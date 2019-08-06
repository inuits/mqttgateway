package main

import (
	"fmt"
	oslog "log"
	"os"
	"sync"
	"time"

	pb "github.com/IHI-Energy-Storage/sparkpluggw/Sparkplug"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/golang/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

var mutex sync.RWMutex
var edgeNodeList map[string]bool

// contants for various SP labels and metric names
const (
	SPPushTotalMetric      string = "sp_total_metrics_pushed"
	SPLastTimePushedMetric string = "sp_last_pushed_timestamp"
	SPConnectionCount      string = "sp_connection_established_count"
	SPDisconnectionCount   string = "sp_connection_lost_count"
	SPPushInvalidMetric    string = "sp_invalid_metric_name_received"

	SPReincarnationAttempts string = "sp_reincarnation_attempt_count"
	SPReincarnationFailures string = "sp_reincarnation_failure_count"
	SPReincarnationSuccess  string = "sp_reincarnation_success_count"
	SPReincarnationDelay    string = "sp_reincarnation_delayed_count"

	NewMetricString			string = "Creating new SP metric %s\n"

	SPReincarnateTimer  uint32 = 900
	SPReincarnateRetry  uint32 = 60
	SPReconnectionTimer uint32 = 300
	PBInt8              uint32 = 1
	PBInt16             uint32 = 2
	PBInt32             uint32 = 3
	PBInt64             uint32 = 4
	PBUInt8             uint32 = 5
	PBUInt16            uint32 = 6
	PBUInt32            uint32 = 7
	PBUInt64            uint32 = 8
	PBFloat             uint32 = 9
	PBDouble            uint32 = 10
	PBBoolean           uint32 = 11
	PBString            uint32 = 12
	PBDateTime          uint32 = 13
	PBText              uint32 = 14
	PBUUID              uint32 = 15
	PBDataSet           uint32 = 16
	PBBytes             uint32 = 17
	PBFile              uint32 = 18
	PBTemplate          uint32 = 19
	PBPropertySet       uint32 = 20
	PBPropertySetList   uint32 = 21
)

type spplugExporter struct {
	client      mqtt.Client
	versionDesc *prometheus.Desc
	connectDesc *prometheus.Desc

	// Holds the mertrics collected
	metrics        map[string]*prometheus.GaugeVec
	counterMetrics map[string]*prometheus.CounterVec
}

// Initialize
func initSparkPlugExporter() *spplugExporter {

	if *mqttDebug == "true" {
		mqtt.ERROR = oslog.New(os.Stdout, "MQTT ERROR    ", oslog.Ltime)
		mqtt.CRITICAL = oslog.New(os.Stdout, "MQTT CRITICAL ", oslog.Ltime)
		mqtt.WARN = oslog.New(os.Stdout, "MQTT WARNING  ", oslog.Ltime)
		mqtt.DEBUG = oslog.New(os.Stdout, "MQTT DEBUG    ", oslog.Ltime)
	}

	// create a MQTT client
	options := mqtt.NewClientOptions()

	log.Infof("Connecting to %v", *brokerAddress)

	// Set broker and client options
	options.AddBroker(*brokerAddress)
	options.SetClientID(*clientID)

	// Set client timeouts and intervals
	options.SetWriteTimeout(5 * time.Second)
	options.SetPingTimeout(1 * time.Second)
	options.SetMaxReconnectInterval(time.Duration(SPReconnectionTimer))

	// Set handler functions
	options.SetOnConnectHandler(connectHandler)
	options.SetConnectionLostHandler(disconnectHandler)

	// Set capabilities
	options.SetAutoReconnect(true)

	m := mqtt.NewClient(options)

	// create an exporter
	e := &spplugExporter{
		client: m,
		versionDesc: prometheus.NewDesc(
			prometheus.BuildFQName(progname, "build", "info"),
			"Build info of this instance", nil,
			prometheus.Labels{"version": version}),
		connectDesc: prometheus.NewDesc(
			prometheus.BuildFQName(progname, "mqtt", "connected"),
			"Is the exporter connected to mqtt broker", nil, nil),
	}

	log.Debugf("Initializing Exporter Metrics and Data\n")

	e.initializeMetricsAndData()

	if token := m.Connect(); token.Wait() && token.Error() != nil {
		log.Fatal(token.Error())
	}

	m.Subscribe(*topic, 2, e.receiveMessage())
	return e
}

func (e *spplugExporter) Describe(ch chan<- *prometheus.Desc) {
	mutex.RLock()
	defer mutex.RUnlock()
	ch <- e.versionDesc
	ch <- e.connectDesc
	for _, m := range e.counterMetrics {
		m.Describe(ch)
	}
	for _, m := range e.metrics {
		m.Describe(ch)
	}
}

func (e *spplugExporter) Collect(ch chan<- prometheus.Metric) {
	mutex.RLock()
	defer mutex.RUnlock()
	ch <- prometheus.MustNewConstMetric(
		e.versionDesc,
		prometheus.GaugeValue,
		1,
	)

	connected := 0.
	if e.client.IsConnectionOpen() {
		connected = 1.
	}

	ch <- prometheus.MustNewConstMetric(
		e.connectDesc,
		prometheus.GaugeValue,
		connected,
	)

	for _, m := range e.counterMetrics {
		m.Collect(ch)
	}

	for _, m := range e.metrics {
		m.Collect(ch)
	}
}

func (e *spplugExporter) receiveMessage() func(mqtt.Client, mqtt.Message) {
	return func(c mqtt.Client, m mqtt.Message) {
		mutex.Lock()
		defer mutex.Unlock()

		var pbMsg pb.Payload
		var eventString string

		// Unmarshal MQTT message into Google Protocol Buffer
		if err := proto.Unmarshal(m.Payload(), &pbMsg); err != nil {
			log.Errorf("Error decoding GPB, message: %v\n", err)
			return
		}

		topic := m.Topic()
		log.Infof("Received message: %s\n", topic)
		log.Debugf("%s\n", pbMsg.String())

		// Get the labels and value for the labels from the topic and constants
		labels, labelValues, processMetric := prepareLabelsAndValues(topic)

		if !processMetric {
			return
		}

		// Process this edge node, if it is unique start the re-birth process
		e.evaluateEdgeNode(c, labelValues["sp_namespace"],
			labelValues["sp_group_id"],
			labelValues["sp_edge_node_id"])

		// Sparkplug messages contain multiple metrics within them
		// traverse them and process them

		metricList := pbMsg.GetMetrics()

		for _, metric := range metricList {
			metricName, err := getMetricName(metric)
			if  err != nil {
				log.Errorf("Error: %s %s %v  \n", labelValues["sp_edge_node_id"], metricName, err)
				e.counterMetrics[SPPushInvalidMetric].With(labelValues).Inc()
				continue
			}
			if _, ok := e.metrics[metricName]; !ok {

				eventString = "Creating metric"

				e.metrics[metricName] = prometheus.NewGaugeVec(
					prometheus.GaugeOpts{
						Name: metricName,
						Help: "Metric pushed via MQTT",
					},
					labels,
				)
			} else {
				eventString = "Updating metric"
			}

			if metricVal, err := convertMetricToFloat(metric); err != nil {
				log.Debugf("Error %v converting data type for metric %s\n",
					err, metricName)
			} else {
				log.Debugf("%s %s : %g\n", eventString, metricName, metricVal)
				e.metrics[metricName].With(labelValues).Set(metricVal)
				e.metrics[SPLastTimePushedMetric].With(labelValues).SetToCurrentTime()
				e.counterMetrics[SPPushTotalMetric].With(labelValues).Inc()
			}
		}
	}
}

// If the edge node is unique (this is the first time seeing it), then
// issue an NCMD and start the rebirth process so we get a fresh set of all
// the metrics / tags

func (e *spplugExporter) evaluateEdgeNode(c mqtt.Client, namespace string,
	group string, nodeID string) {

	edgeNode := group + "/" + nodeID

	if _, exists := edgeNodeList[edgeNode]; !exists {
		edgeNodeList[edgeNode] = true
		e.reincarnate(namespace, group, nodeID)
	} else {
		log.Debugf("Known edge node: %s\n", edgeNode)
	}
}

func (e *spplugExporter) reincarnate(namespace string, group string,
	nodeID string) {
	go func() {
		var pbMsg pb.Payload
		var pbMetric pb.Payload_Metric
		var pbMetricList []*pb.Payload_Metric
		var pbValue pb.Payload_Metric_BooleanValue

		_, labelValues := getNodeLabelSetandValues(namespace, group, nodeID)

		metricName := "Node Control/Rebirth"
		dataType := PBBoolean

		pbValue.BooleanValue = true
		pbMetric.Name = &metricName
		pbMetric.Datatype = &dataType
		pbMetric.Value = &pbValue

		pbMetricList = append(pbMetricList, &pbMetric)
		pbMsg.Metrics = pbMetricList

		topic := namespace + "/" + group + "/NCMD/" + nodeID

		for true {
			if e.client.IsConnectionOpen() {
				log.Infof("Reincarnate: %s\n", topic)

				e.counterMetrics[SPReincarnationAttempts].
					With(labelValues).Inc()

				timestamp := uint64(time.Now().UnixNano() / 1000000)
				pbMsg.Timestamp = &timestamp

				if sendMQTTMsg(e.client, &pbMsg, topic) {
					e.counterMetrics[SPReincarnationSuccess].
						With(labelValues).Inc()
				} else {
					e.counterMetrics[SPReincarnationFailures].
						With(labelValues).Inc()
				}

				time.Sleep(time.Duration(SPReincarnateTimer) * time.Second)
			} else {
				e.counterMetrics[SPReincarnationDelay].With(labelValues).Inc()
				time.Sleep(time.Duration(SPReincarnateRetry) * time.Second)
			}
		}
	}()
}

func (e *spplugExporter) initializeMetricsAndData() {

	e.metrics = make(map[string]*prometheus.GaugeVec)
	e.counterMetrics = make(map[string]*prometheus.CounterVec)

	edgeNodeList = make(map[string]bool)

	labels := getLabelSet()
	serviceLabels, _ := getServiceLabelSetandValues()
	nodelabels := getNodeLabelSet()

	log.Debugf(NewMetricString, SPPushTotalMetric)

	e.counterMetrics[SPPushTotalMetric] = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: SPPushTotalMetric,
			Help: fmt.Sprintf("Number of messages published on a MQTT topic"),
		},
		labels,
	)

	log.Debugf(NewMetricString, SPLastTimePushedMetric)

	e.metrics[SPLastTimePushedMetric] = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: SPLastTimePushedMetric,
			Help: fmt.Sprintf("Last time a metric was pushed to a MQTT topic"),
		},
		labels,
	)

	log.Debugf(NewMetricString, SPConnectionCount)

	e.counterMetrics[SPConnectionCount] = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: SPConnectionCount,
			Help: fmt.Sprintf("Total MQTT connections established"),
		},
		serviceLabels,
	)

	log.Debugf(NewMetricString, SPDisconnectionCount)

	e.counterMetrics[SPDisconnectionCount] = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: SPDisconnectionCount,
			Help: fmt.Sprintf("Total MQTT disconnections"),
		},
		serviceLabels,
	)

	log.Debugf(NewMetricString, SPPushInvalidMetric)

	e.counterMetrics[SPPushInvalidMetric] = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: SPPushInvalidMetric,
			Help: fmt.Sprintf("Non-compliant prometheus metrics added in the list"),
		},
		serviceLabels,
	)

	log.Debugf(NewMetricString, SPReincarnationAttempts)

	e.counterMetrics[SPReincarnationAttempts] = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: SPReincarnationAttempts,
			Help: fmt.Sprintf("Total NCMD message attempts"),
		},
		nodelabels,
	)

	log.Debugf(NewMetricString, SPReincarnationFailures)

	e.counterMetrics[SPReincarnationFailures] = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: SPReincarnationFailures,
			Help: fmt.Sprintf("Total NCMD message failures"),
		},
		nodelabels,
	)

	log.Debugf(NewMetricString, SPReincarnationSuccess)

	e.counterMetrics[SPReincarnationSuccess] = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: SPReincarnationSuccess,
			Help: fmt.Sprintf("Total successful NCMD attempts"),
		},
		nodelabels,
	)

	log.Debugf(NewMetricString, SPReincarnationDelay)

	e.counterMetrics[SPReincarnationDelay] = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: SPReincarnationDelay,
			Help: fmt.Sprintf("Total delayed NCMD attempts due to connection issues"),
		},
		nodelabels,
	)
}
