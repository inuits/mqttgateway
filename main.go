package main

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/log"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	listenAddress = kingpin.Flag("web.listen-address", "Address on which to expose metrics and web interface.").Default(":9337").String()
	metricsPath   = kingpin.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").String()
	brokerAddress = kingpin.Flag("mqtt.broker-address", "Address of the MQTT broker.").Envar("MQTT_BROKER_ADDRESS").Default("tcp://localhost:1883").String()
	topic         = kingpin.Flag("mqtt.topic", "MQTT topic to subscribe to").Envar("MQTT_TOPIC").Default("prometheus/#").String()
	prefix        = kingpin.Flag("mqtt.prefix", "MQTT topic prefix to remove when creating metrics").Envar("MQTT_PREFIX").Default("prometheus").String()
	username      = kingpin.Flag("mqtt.username", "MQTT username").Envar("MQTT_USERNAME").String()
	password      = kingpin.Flag("mqtt.password", "MQTT password").Envar("MQTT_PASSWORD").String()
	clientID      = kingpin.Flag("mqtt.clientid", "MQTT client ID").Envar("MQTT_CLIENT_ID").String()
	progname      = "mqttgateway"
)

func main() {
	log.AddFlags(kingpin.CommandLine)
	kingpin.Parse()

	prometheus.MustRegister(newMQTTExporter())

	http.Handle(*metricsPath, promhttp.Handler())
	log.Infoln("Listening on", *listenAddress)
	err := http.ListenAndServe(*listenAddress, nil)
	if err != nil {
		log.Fatal(err)
	}
}
