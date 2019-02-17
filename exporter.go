package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	pb "github.com/IHI-Energy-Storage/sparkpluggw/Sparkplug"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/golang/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

var mutex sync.RWMutex

// contants for various SP labels and metric names
const (
	SPPushTotalMetric      string = "sp_total_metrics_pushed"
	SPLastTimePushedMetric string = "sp_last_pushed_timestamp"
	SPTopicLabel           string = "sp_topic"
)

type spplugExporter struct {
	client      mqtt.Client
	versionDesc *prometheus.Desc
	connectDesc *prometheus.Desc

	// Holds the mertrics collected
	metrics        map[string]*prometheus.GaugeVec
	counterMetrics map[string]*prometheus.CounterVec
}

func newMQTTExporter() *spplugExporter {
	// create a MQTT client
	options := mqtt.NewClientOptions()

	log.Infof("Connecting to %v", *brokerAddress)
	options.AddBroker(*brokerAddress)
	options.SetClientID(*clientID)

	m := mqtt.NewClient(options)
	if token := m.Connect(); token.Wait() && token.Error() != nil {
		log.Fatal(token.Error())
	}

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

	e.metrics = make(map[string]*prometheus.GaugeVec)
	e.counterMetrics = make(map[string]*prometheus.CounterVec)

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
	if e.client.IsConnected() {
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
			log.Errorf("Error decoding GPB ,message: %v\n", err)
			return
		}

		topic := m.Topic()
		log.Debugf("Received message from topic: %s", topic)

		// Get the labels and value for the labels from the topic and constants
		labels, labelValues, processMetric := prepareLabelsAndValues(topic)

		if processMetric != true {
			return
		}

		if _, ok := e.counterMetrics[SPPushTotalMetric]; !ok {
			log.Debugf("Creating new SP metric %s for %s\n", SPPushTotalMetric, topic)

			e.counterMetrics[SPPushTotalMetric] = prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: SPPushTotalMetric,
					Help: fmt.Sprintf("Number of messages published on topic %s", topic),
				},
				labels,
			)
		}

		if _, ok := e.metrics[SPLastTimePushedMetric]; !ok {
			log.Debugf("Creating new SP metric %s for %s\n", SPLastTimePushedMetric,
				topic)

			e.metrics[SPLastTimePushedMetric] = prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: SPLastTimePushedMetric,
					Help: fmt.Sprintf("Last time a metric was pushed to topic %s", topic),
				},
				labels,
			)
		}

		// Sparkplug messages contain multiple metrics within them
		// traverse them and process them
		metricList := pbMsg.GetMetrics()

		for _, metric := range metricList {
			metricName := metric.GetName()

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

func prepareLabelsAndValues(topic string) ([]string, prometheus.Labels, bool) {
	t := strings.TrimPrefix(topic, *prefix)
	t = strings.TrimPrefix(t, "/")
	parts := strings.Split(t, "/")

	// 6.1.3 covers 9 message types, only process device data
	// Sparkplug puts 5 key namespacing elements in the topic name
	// these are being parsed and will be added as metric labels

	if (parts[2] == "DDATA") || (parts[2] == "DBIRTH") {
		if len(parts) != 5 {
			log.Debugf("Ignoring topic %s, does not comply with Sparkspec\n", t)
			return nil, nil, false
		}
	} else {
		log.Debugf("Ignoring non-device metric data: %s\n", parts[2])
		return nil, nil, false
	}

	/* See the sparkplug definition for the topic construction */
	/** Set the Prometheus labels to their corresponding topic part **/

	var labels = []string{"sp_namespace", "sp_group_id",
		"sp_msgtype", "sp_edge_node_id", "sp_device_id"}

	labelValues := prometheus.Labels{}

	// Labels are created from the topic parsing above and compared against
	// the set of labels for this metric.   If this is a unique set then it will
	// be stored and the metric will be treated as unique and new.   If the
	// metric and label set is not new, it will be updated.
	//
	// The logic for this is that the same metric name could used across
	// topics (same metric posted for different devices)

	for i, l := range labels {
		labelValues[l] = parts[i]
	}

	// Add the topic as a label to the metrics
	labels = append(labels, SPTopicLabel)
	labelValues[SPTopicLabel] = topic

	return labels, labelValues, true
}

func convertMetricToFloat(metric *pb.Payload_Metric) (float64, error) {
	var errUnexpectedType = errors.New("Non-numeric type could not be converted to float")

	const (
		PBInt8   uint32 = 1
		PBInt16  uint32 = 2
		PBInt32  uint32 = 3
		PBInt64  uint32 = 4
		PBUInt8  uint32 = 5
		PBUInt16 uint32 = 6
		PBUInt32 uint32 = 7
		PBUInt64 uint32 = 8
		PBFloat  uint32 = 9
		PBDouble uint32 = 10
	)

	switch metric.GetDatatype() {
	case PBInt8:
		tmpLong := metric.GetIntValue()
		tmpSigned := int8(tmpLong)
		return float64(tmpSigned), nil
	case PBInt16:
		tmpLong := metric.GetIntValue()
		tmpSigned := int16(tmpLong)
		return float64(tmpSigned), nil
	case PBInt32:
		tmpLong := metric.GetIntValue()
		tmpSigned := int32(tmpLong)
		return float64(tmpSigned), nil
	case PBUInt8:
		return float64(metric.GetIntValue()), nil
	case PBUInt16:
		return float64(metric.GetIntValue()), nil
	case PBUInt32:
		return float64(metric.GetIntValue()), nil
	case PBInt64:
		// This exists because there is an unsigned consersion that
		// occurs, so moving it to an int64 allows for the sign to work properly
		tmpLong := metric.GetLongValue()
		tmpSigned := int64(tmpLong)
		return float64(tmpSigned), nil
	case PBUInt64:
		return float64(metric.GetLongValue()), nil
	case PBFloat:
		return float64(metric.GetFloatValue()), nil
	case PBDouble:
		return float64(metric.GetDoubleValue()), nil
	default:
		return float64(0), errUnexpectedType
	}
}
