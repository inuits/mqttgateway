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
	"fmt"
	"strconv"
	"strings"
	"sync"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

var mutex sync.RWMutex

type mqttExporter struct {
	client         mqtt.Client
	connectDesc    *prometheus.Desc
	metrics        map[string]*prometheus.GaugeVec   // hold the metrics collected
	counterMetrics map[string]*prometheus.CounterVec // hold the metrics collected
	metricsLabels  map[string][]string               // holds the labels set for each metric to be able to invalidate them
}

func newMQTTExporter() *mqttExporter {
	// create a MQTT client
	options := mqtt.NewClientOptions()
	log.Infof("Connecting to %v", *brokerAddress)
	options.AddBroker(*brokerAddress)
	if *username != "" {
		options.SetUsername(*username)
	}
	if *password != "" {
		options.SetPassword(*password)
	}
	if *clientID != "" {
		options.SetClientID(*clientID)
	}
	m := mqtt.NewClient(options)
	if token := m.Connect(); token.Wait() && token.Error() != nil {
		log.Fatal(token.Error())
	}

	// create an exporter
	c := &mqttExporter{
		client: m,
		connectDesc: prometheus.NewDesc(
			prometheus.BuildFQName(progname, "mqtt", "connected"),
			"Is the exporter connected to mqtt broker",
			nil,
			nil),
	}

	c.metrics = make(map[string]*prometheus.GaugeVec)
	c.counterMetrics = make(map[string]*prometheus.CounterVec)
	c.metricsLabels = make(map[string][]string)

	m.Subscribe(*topic, 2, c.receiveMessage())

	return c
}

func (c *mqttExporter) Describe(ch chan<- *prometheus.Desc) {
	mutex.RLock()
	defer mutex.RUnlock()
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
	connected := 0.
	if c.client.IsConnectionOpen() {
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
		t := strings.TrimPrefix(m.Topic(), *prefix)
		t = strings.TrimPrefix(t, "/")
		parts := strings.Split(t, "/")
		if len(parts)%2 == 0 {
			log.Warnf("Invalid topic: %s: odd number of levels, ignoring", t)
			return
		}
		metric_name := parts[len(parts)-1]
		pushed_metric_name := fmt.Sprintf("mqtt_%s_last_pushed_timestamp", metric_name)
		count_metric_name := fmt.Sprintf("mqtt_%s_push_total", metric_name)
		metric_labels := parts[:len(parts)-1]
		var labels []string
		labelValues := prometheus.Labels{}
		log.Debugf("Metric name: %v", metric_name)
		for i, l := range metric_labels {
			if i%2 == 1 {
				continue
			}
			labels = append(labels, l)
			labelValues[l] = metric_labels[i+1]
		}

		invalidate := false
		if _, ok := e.metricsLabels[metric_name]; ok {
			l := e.metricsLabels[metric_name]
			if !compareLabels(l, labels) {
				log.Warnf("Label names are different: %v and %v, invalidating existing metric", l, labels)
				prometheus.Unregister(e.metrics[metric_name])
				invalidate = true
			}
		}
		e.metricsLabels[metric_name] = labels
		if _, ok := e.metrics[metric_name]; ok && !invalidate {
			log.Debugf("Metric already exists")
		} else {
			log.Debugf("Creating new metric: %s %v", metric_name, labels)
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
		if s, err := strconv.ParseFloat(string(m.Payload()), 64); err == nil {
			e.metrics[metric_name].With(labelValues).Set(s)
			e.metrics[pushed_metric_name].With(labelValues).SetToCurrentTime()
			e.counterMetrics[count_metric_name].With(labelValues).Inc()
		}
	}
}

func compareLabels(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
