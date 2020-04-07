package main

import (
	"errors"
	"strings"

	pb "github.com/IHI-Energy-Storage/sparkpluggw/Sparkplug"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/golang/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/model"
)

// contants for various SP labels and metric names
const (
	SPNamespace  string = "sp_namespace"
	SPGroupID    string = "sp_group_id"
	SPEdgeNodeID string = "sp_edge_node_id"
	SPDeviceID   string = "sp_device_id"
	SPMQTTTopic  string = "sp_mqtt_topic"
	SPMQTTServer string = "sp_mqtt_server"
)

func sendMQTTMsg(c mqtt.Client, pbMsg *pb.Payload,
	topic string) bool {

	msg, err := proto.Marshal(pbMsg)

	if err != nil {
		log.Warnf("Failed to Marshall: %s\n", err)
		return false
	}

	token := c.Publish(topic, 0, false, msg)
	token.Wait()
	log.Debugf("%s\n", pbMsg.String())

	return true
}

func cloneLabelSet(labels prometheus.Labels) prometheus.Labels {
	newLabels := prometheus.Labels{}

	for key, value := range labels {
		newLabels[key] = value
	}

	return newLabels
}

// In order for 2 label sets to match, they have to have the exact same
// number of entries and the exact same entries orthogonal or the order
// that they are stored

func compareLabelSet(metricSet []prometheusmetric,
	newLabels []string) (bool, int) {
	returnCode := false
	returnIndex := 0
	tmpIndex := 0
	for _, existingMetric := range metricSet {

		// Make sure that both label sets have the same number of entries
		if len(existingMetric.promlabel) == len(newLabels) {

			// Initially we believe all labeles are unverified
			// As we verify we decrement, if we end up with something > 0
			// we know the set does not match

			mismatchedLabels := len(newLabels)

			for _, newLabel := range newLabels {
				// Compare the current new label to everything in existing
				// label set
				for _, existingLabel := range existingMetric.promlabel {
					if existingLabel == newLabel {
						mismatchedLabels--
						break
					}
				}
			}

			if mismatchedLabels == 0 {
				returnCode = true
				returnIndex = tmpIndex
			}
		}

		tmpIndex++
	}
	return returnCode, returnIndex
}

func createNewMetric(metricName string, metricLabels []string) *prometheus.GaugeVec {
	var newMetric prometheusmetric

	newMetric.prommetric = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricName,
			Help: "Metric pushed via MQTT",
		},
		metricLabels,
	)
	return newMetric.prommetric
}

func prepareLabelsAndValues(topic string) ([]string, prometheus.Labels, bool) {
	var labels []string
	t := strings.TrimPrefix(topic, *prefix)
	t = strings.TrimPrefix(t, "/")
	parts := strings.Split(t, "/")
	log.Debugf("Metric after spliting the wrt / and its length: %s: %d\n", parts, len(parts))

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
	if labels == nil {
		labels = getLabelSet()
	}

	labelValues := prometheus.Labels{}
	log.Debugf("Label values it recieve with prometheus.Labels{}: %s\n", prometheus.Labels{})
	// Labels are created from the topic parsing above and compared against
	// the set of labels for this metric.   If this is a unique set then it will
	// be stored and the metric will be treated as unique and new.   If the
	// metric and label set is not new, it will be updated.
	//
	// The logic for this is that the same metric name could used across
	// topics (same metric posted for different devices)

	labelValues[SPNamespace] = parts[0]
	labelValues[SPGroupID] = parts[1]
	labelValues[SPEdgeNodeID] = parts[3]
	labelValues[SPDeviceID] = parts[4]

	return labels, labelValues, true
}

func getLabelSet() []string {
	return []string{SPNamespace, SPGroupID, SPEdgeNodeID, SPDeviceID}
}

func getServiceLabelSetandValues() ([]string, map[string]string) {
	labels := []string{SPMQTTTopic, SPMQTTServer}

	labelValues := map[string]string{
		SPMQTTTopic:  *topic,
		SPMQTTServer: *brokerAddress,
	}

	return labels, labelValues
}

func getNodeLabelSetandValues(namespace string, group string,
	nodeID string) ([]string, map[string]string) {
	labels := getNodeLabelSet()
	labelValues := map[string]string{
		SPNamespace:  namespace,
		SPGroupID:    group,
		SPEdgeNodeID: nodeID,
	}

	return labels, labelValues
}

func getNodeLabelSet() []string {
	return []string{SPNamespace, SPGroupID, SPEdgeNodeID}
}
// This function acceptys MQTT metric message,
// extracts out the nested folders(if any), add those folder names in Key value labels
// and return label value sets, metrics wrt to those labelvalues and error(if any)
func getMetricName(metric *pb.Payload_Metric) ([]string, string, error) {
	var errUnexpectedType error
	var labelvalues []string

	metricName := metric.GetName()

	if strings.Contains(metricName, "/") == true && metricName != "Device Control/Rebirth" {
		parts := strings.Split(metricName, "/")
		size := len(parts)
		metricName = parts[size-1]
		for metlen := 0; metlen <= size-2; metlen++ {
			labelvalues = append(labelvalues, parts[metlen])

		}
		log.Debugf("Received message for labelvalues: %s\n", labelvalues)
	}
	metricNameL := model.LabelValue(metricName)

	if model.IsValidMetricName(metricNameL) == true {
		errUnexpectedType = nil
	} else {
		errUnexpectedType = errors.New("Non-compliant metric name")
	}

	return []string(labelvalues), string(metricNameL), errUnexpectedType
}

func convertMetricToFloat(metric *pb.Payload_Metric) (float64, error) {
	var errUnexpectedType = errors.New("Non-numeric type could not be converted to float")

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
