package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	pb "github.com/IHI-Energy-Storage/mqttgateway/Sparkplug"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/golang/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

var mutex sync.RWMutex

type mqttExporter struct {
	client      mqtt.Client
	versionDesc *prometheus.Desc
	connectDesc *prometheus.Desc

	// Holds the mertrics collected
	metrics        map[string]*prometheus.GaugeVec
	counterMetrics map[string]*prometheus.CounterVec

	// Holds the labels.  Note that because the same metric can be set across
	// topics, each unique set of labels are stored with that metric
	metricsLabels map[string][]prometheus.Labels
}

func newMQTTExporter() *mqttExporter {
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
	e := &mqttExporter{
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
	e.metricsLabels = make(map[string][]prometheus.Labels)

	m.Subscribe(*topic, 2, e.receiveMessage())
	return e
}

func (e *mqttExporter) Describe(ch chan<- *prometheus.Desc) {
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

func (e *mqttExporter) Collect(ch chan<- prometheus.Metric) {
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

func (e *mqttExporter) receiveMessage() func(mqtt.Client, mqtt.Message) {
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

		// Sparkplug puts 5 key namespacing elements in the topic name
		// these are being parsed and will be added as metric labels

		t := strings.TrimPrefix(m.Topic(), *prefix)
		t = strings.TrimPrefix(t, "/")
		parts := strings.Split(t, "/")

		log.Debugf("Received message from topic: %s", t)
		log.Debugf("{\n%s\n}\n", pbMsg.String())

		// 6.1.3 covers 9 message types, only process device data
		if (parts[2] == "DDATA") || (parts[2] == "DBIRTH") {
			if len(parts) != 5 {
				log.Debugf("Ignoring topic %s, does not comply with Sparkspec\n", t)
				return
			}
		} else {
			log.Debugf("Ignoring non-device metric data: %s\n", parts[2])
			return
		}

		/* See the sparkplug definition for the topic construction */
		/** Set the Prometheus labels to their corresponding topic part **/

		//
		var labels = []string{"sp_namespace", "sp_group_id", "sp_msgtype",
			"sp_edge_node_id", "sp_device_id"}

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

		/**  Sparkplug messages contain multiple metrics within them **/
		/** traverse them and process them                           **/
		metricList := pbMsg.GetMetrics()

		for _, metric := range metricList {
			metricName := metric.GetName()
			pushedMetricName :=
				fmt.Sprintf("mqtt_%s_last_pushed_timestamp", metricName)
			countMetricName :=
				fmt.Sprintf("mqtt_%s_push_total", metricName)

			existingMetric := false

			if _, ok := e.metricsLabels[metricName]; ok {
				for _, l := range e.metricsLabels[metricName] {

					log.Debugf("Comparing Labels: %v and %v\n", l, labelValues)

					if reflect.DeepEqual(l, labelValues) {
						existingMetric = true
						break
					}
				}
			}

			if _, ok := e.metrics[metricName]; ok && existingMetric {
				eventString = "Updating metric"
			} else {
				e.metricsLabels[metricName] =
					append(e.metricsLabels[metricName], labelValues)

				eventString = "Creating metric"
				e.metrics[metricName] = prometheus.NewGaugeVec(
					prometheus.GaugeOpts{
						Name: metricName,
						Help: "Metric pushed via MQTT",
					},
					labels,
				)
				e.counterMetrics[countMetricName] = prometheus.NewCounterVec(
					prometheus.CounterOpts{
						Name: countMetricName,
						Help: fmt.Sprintf("Number of times %s was pushed via MQTT", metricName),
					},
					labels,
				)
				e.metrics[pushedMetricName] = prometheus.NewGaugeVec(
					prometheus.GaugeOpts{
						Name: pushedMetricName,
						Help: fmt.Sprintf("Last time %s was pushed via MQTT", metricName),
					},
					labels,
				)
			}

			if metricVal, err := convertMetricToFloat(metric); err != nil {
				log.Debugf("Error %v converting data type for metric %s\n",
					err, metricName)
			} else {
				log.Debugf("%s %s : %g\n", eventString, metricName, metricVal)
				log.Debugf("Labels: %v\n", labelValues)
				e.metrics[metricName].With(labelValues).Set(metricVal)
				e.metrics[pushedMetricName].With(labelValues).SetToCurrentTime()
				e.counterMetrics[countMetricName].With(labelValues).Inc()
			}
		}
	}
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
		return float64(metric.GetIntValue()), nil
	case PBInt16:
		return float64(metric.GetIntValue()), nil
	case PBInt32:
		return float64(metric.GetIntValue()), nil
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
