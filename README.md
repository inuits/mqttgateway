# MQTTGateway for Prometheus

[![circleci](https://circleci.com/gh/inuits/mqttgateway/tree/master.svg?style=shield)][circleci]
[![go report card](https://goreportcard.com/badge/github.com/inuits/mqttgateway)][goreportcard]

A project that subscribes to MQTT queues and published prometheus metrics.

```
usage: mqttgateway [<flags>]

Flags:
  --help                        Show context-sensitive help (also try --help-long and --help-man).
  --web.listen-address=":9337"  Address on which to expose metrics and web interface.
  --web.telemetry-path="/metrics"
                                Path under which to expose metrics.
  --mqtt.broker-address="tcp://localhost:1883"
                                Address of the MQTT broker.
                                The default is taken from $MQTT_BROKER_ADDRESS if it is set.
  --mqtt.topic="prometheus/#"   MQTT topic to subscribe to.
                                The default is taken from $MQTT_TOPIC if it is set.
  --mqtt.prefix="prometheus"    MQTT topic prefix to remove when creating metrics.
                                The default is taken from $MQTT_PREFIX if it is set.
  --mqtt.username=""            MQTT username.
                                The default is taken from $MQTT_USERNAME if it is set.
  --mqtt.password=""            MQTT password.
                                The default is taken from $MQTT_PASSWORD if it is set.
  --mqtt.clientid=""            MQTT client ID.
                                The default is taken from $MQTT_CLIENT_ID if it is set.
  --log.level="info"            Only log messages with the given severity or above. Valid levels: [debug, info, warn, error, fatal]
  --log.format="logger:stderr"  Set the log target and format. Example: "logger:syslog?appname=bob&local=7" or "logger:stdout?json=true"
```

## Installation from source

Requires go

```
go get -u github.com/inuits/mqttgateway
```

## Installation with Docker

[![dockeri.co](https://dockeri.co/image/mqttgateway/mqttgateway)](https://hub.docker.com/r/mqttgateway/mqttgateway)

Use a MQTT broker which runs locally:

```
$ docker run --network="host" -p 9337:9337 mqttgateway/mqttgateway
```

Use a MQTT broker which runs elsewhere:

```
$ docker run -p 9337:9337 mqttgateway/mqttgateway --mqtt.broker-address="tcp://mosquitto.local:1883"
```


## How does it work?

mqttgateway will connect to the MQTT broker at `--mqtt.broker-address` and
listen to the topics specified by `--mqtt.topic`.

By default, it will listen to `prometheus/#`.

The format for the topics is as follow:

`prefix/LABEL1/VALUE1/LABEL2/VALUE2/NAME`

A topic `prometheus/job/ESP8266/instance/livingroom/temperature_celsius` would
be converted to a metric
`temperature_celsius{job="ESP8266",instance="livingroom"}`.

If labelnames differ for a same metric, then we invalidate existing metrics and
only keep new ones. Then we issue a warning in the logs. You should avoid it.

Two other metrics are published, for each metric:

- `mqtt_NAME_last_pushed_timestamp`, the last time NAME metric has been pushed
(unix time, in seconds)
- `mqtt_NAME_push_total`, the number of times a metric has been pushed

## Security

This project does not support authentication yet but that is planned.

## Example

```
$ mosquitto_pub -m 20.2 -t prometheus/job/ESP8266/instance/livingroom/temperature_celsius
$ curl -s http://127.0.0.1:9337/metrics|grep temperature_celsius|grep -v '#'
mqtt_temperature_celsius_last_pushed_timestamp{instance="livingroom",job="ESP8266"} 1.525185129171293e+09
mqtt_temperature_celsius_push_total{instance="livingroom",job="ESP8266"} 1
temperature_celsius{instance="livingroom",job="ESP8266"} 20.2
```

## A note about the prometheus config

If you use `job` and `instance` labels, please refer to the [pushgateway
exporter
documentation](https://github.com/prometheus/pushgateway#about-the-job-and-instance-labels).

TL;DR: you should set `honor_labels: true` in the scrape config.

[circleci]:https://circleci.com/gh/inuits/mqttgateway
[goreportcard]:https://goreportcard.com/report/github.com/inuits/mqttgateway
