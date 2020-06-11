// Copyright 2020 The MQTTGateway authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
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

	prometheus.MustRegister(version.NewCollector("mqttgateway"))
	prometheus.MustRegister(newMQTTExporter())

	http.Handle(*metricsPath, promhttp.Handler())
	log.Infoln("Listening on", *listenAddress)
	err := http.ListenAndServe(*listenAddress, nil)
	if err != nil {
		log.Fatal(err)
	}
}
