package main

import (
	"fmt"
	"strings"
	"sync"
  "reflect"
  //"runtime"
  "errors"
  //"math"
  //"strconv"
  //"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/prometheus/client_golang/prometheus"
    "github.com/golang/protobuf/proto"
	"github.com/prometheus/common/log"
	pb "github.com/IHI-Energy-Storage/mqttgateway/Sparkplug"
)

var mutex sync.RWMutex


type mqttExporter struct {
	client         mqtt.Client
	versionDesc    *prometheus.Desc
	connectDesc    *prometheus.Desc
	metrics        map[string]*prometheus.GaugeVec   // hold the metrics collected
	counterMetrics map[string]*prometheus.CounterVec // hold the metrics collected
	metricsLabels  map[string][]prometheus.Labels
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
	c := &mqttExporter{
		client: m,
		versionDesc: prometheus.NewDesc(
			prometheus.BuildFQName(progname, "build", "info"),
			"Build info of this instance",
			nil,
			prometheus.Labels{"version": version}),
		connectDesc: prometheus.NewDesc(
			prometheus.BuildFQName(progname, "mqtt", "connected"),
			"Is the exporter connected to mqtt broker",
			nil,
			nil),
	}

	c.metrics = make(map[string]*prometheus.GaugeVec)
	c.counterMetrics = make(map[string]*prometheus.CounterVec)
	c.metricsLabels = make(map[string][]prometheus.Labels)

  m.Subscribe(*topic, 2, c.receiveMessage())
	return c
}

func (c *mqttExporter) Describe(ch chan<- *prometheus.Desc) {
	mutex.RLock()
	defer mutex.RUnlock()
	ch <- c.versionDesc
	ch <- c.connectDesc
	for _, m := range c.counterMetrics {
		m.Describe(ch)
	}
	for _, m := range c.metrics {
		m.Describe(ch)
	}
}

func (c *mqttExporter) Collect(ch chan<- prometheus.Metric) {
	mutex.RLock()
	defer mutex.RUnlock()
	ch <- prometheus.MustNewConstMetric(
		c.versionDesc,
		prometheus.GaugeValue,
		1,
	)
	connected := 0.
	if c.client.IsConnected() {
		connected = 1.
	}
	ch <- prometheus.MustNewConstMetric(
		c.connectDesc,
		prometheus.GaugeValue,
		connected,
	)
	for _, m := range c.counterMetrics {
		m.Collect(ch)
	}
	for _, m := range c.metrics {
		m.Collect(ch)
	}
}

func (e *mqttExporter) receiveMessage() func(mqtt.Client, mqtt.Message) {
	return func(c mqtt.Client, m mqtt.Message) {
		mutex.Lock()
		defer mutex.Unlock()

    var pb_msg pb.Payload
    var event_string string

    // Unmarshal MQTT message into Google Protocol Buffer
    if err := proto.Unmarshal(m.Payload(), &pb_msg); err != nil {
      log.Errorf("Error decoding GPB ,message: %v\n", err)
      return
    }

    /** Sparkplug puts 5 key namespacing elements in the topic name **/
    /** these are being parsed and will be added as metric labels   **/
    t := m.Topic()
		t = strings.TrimPrefix(m.Topic(), *prefix)
		t = strings.TrimPrefix(t, "/")
		parts := strings.Split(t, "/")

    log.Debugf("Received message from topic: %s", t)
    log.Debugf("{\n%s\n}\n", pb_msg.String())

    /* See the sparkplug definition for the topic construction */
    if (len(parts) != 5) {
      log.Warnf("Invalid topic %s, does not comply with Sparkspec", t);
      return
    }

    /** 6.1.3 covers 9 message types, only process device data **/
    if (parts[2] != "DDATA") {
      log.Debugf("Ignoring non-device metric data: %s", parts[2])
    }

    /** Set the Prometheus labels to their corresponding topic part **/
    var labels = []string {"sp_namespace", "sp_group_id", "sp_msgtype",
                       "sp_edge_node_id", "sp_device_id"}

    labelValues := prometheus.Labels{}

    for i, l := range labels {
      labelValues[l] = parts[i]
    }

    /**  Sparkplug messages contain multiple metrics within them **/
    /** traverse them and process them                           **/
    metric_list := pb_msg.GetMetrics()

    for _,metric := range metric_list {
      metric_name := metric.GetName()
      pushed_metric_name :=
              fmt.Sprintf("mqtt_%s_last_pushed_timestamp", metric_name)
      count_metric_name :=
              fmt.Sprintf("mqtt_%s_push_total", metric_name)

      existing_metric := false

      if _, ok := e.metricsLabels[metric_name]; ok {
        for _, l := range e.metricsLabels[metric_name] {

          log.Debugf("Comparing Labels: %v and %v\n", l, labelValues)

          if reflect.DeepEqual(l, labelValues) {
            existing_metric = true
            break
          }
        }
      }

  		if _, ok := e.metrics[metric_name]; ok && existing_metric {
  			//log.Debugf("Metric %s already exists", metric_name)
        event_string = "Updating metric"
  		} else {
        e.metricsLabels[metric_name] =
            append(e.metricsLabels[metric_name], labelValues)

			  //log.Debugf("Creating new metric: %s", metric_name)
        event_string = "Creating metric"
  			e.metrics[metric_name] = prometheus.NewGaugeVec(
  				prometheus.GaugeOpts{
  					Name: metric_name,
  					Help: "Metric pushed via MQTT",
  				},
  				labels,
  			)
        e.counterMetrics[count_metric_name] = prometheus.NewCounterVec(
    				prometheus.CounterOpts{
    					Name: count_metric_name,
    					Help: fmt.Sprintf("Number of times %s was pushed via MQTT", metric_name),
    				},
    				labels,
    			)
    			e.metrics[pushed_metric_name] = prometheus.NewGaugeVec(
    				prometheus.GaugeOpts{
    					Name: pushed_metric_name,
    					Help: fmt.Sprintf("Last time %s was pushed via MQTT", metric_name),
    				},
    				labels,
    			)
      }

      if metric_val,err := convertMetricToFloat(metric) ; err != nil {
        log.Errorf("Error %v converting data type for metric %s\n",
                  err, metric_name)
      } else {
        log.Debugf("%s %s : %g\n", event_string, metric_name, metric_val)
        log.Debugf("Labels: %v\n", labelValues)
          e.metrics[metric_name].With(labelValues).Set(metric_val)
          e.metrics[pushed_metric_name].With(labelValues).SetToCurrentTime()
          e.counterMetrics[count_metric_name].With(labelValues).Inc()
      }
    }
	}
}

func convertMetricToFloat(metric *pb.Payload_Metric) (float64, error) {
  var errUnexpectedType =
      errors.New("Non-numeric type could not be converted to float")

  const (
    PB_Int8 uint32 = 1;
    PB_Int16 uint32 = 2;
    PB_Int32 uint32 = 3;
    PB_Int64 uint32 = 4;
    PB_UInt8 uint32 = 5;
    PB_UInt16 uint32 = 6;
    PB_UInt32 uint32 = 7;
    PB_UInt64 uint32 = 8;
    PB_Float uint32 = 9;
    PB_Double uint32 = 10;
  )

  switch metric.GetDatatype() {
    case PB_Int8:
      return float64(metric.GetIntValue()), nil
    case PB_Int16:
      return float64(metric.GetIntValue()), nil
    case PB_Int32:
      return float64(metric.GetIntValue()), nil
    case PB_UInt8:
      return float64(metric.GetIntValue()), nil
    case PB_UInt16:
      return float64(metric.GetIntValue()), nil
    case PB_UInt32:
      return float64(metric.GetIntValue()), nil
    case PB_Int64:
      return float64(metric.GetLongValue()), nil
    case PB_UInt64:
      return float64(metric.GetLongValue()), nil
    case PB_Float:
      return float64(metric.GetFloatValue()), nil
    case PB_Double:
      return float64(metric.GetDoubleValue()), nil
    default:
      return float64(0), errUnexpectedType
  }
}
