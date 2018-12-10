# Spark Plug Gateway for Prometheus

A project that subscribes to MQTT queues, reads / parses Spark Plug messages and pushes them as Prometheus metrics.

A project that subscribes to MQTT queues and published prometheus metrics.

```
usage: sparkpluggw [<flags>]

Flags:
  --help                        Show context-sensitive help (also try
--help-long and --help-man).
  --web.listen-address=":9337"  Address on which to expose metrics and web
interface.
  --mqtt.client-id=""              MQTT client identifier (limit to 23 chars)
  --web.telemetry-path="/metrics"
                                Path under which to expose metrics.
  --mqtt.broker-address="tcp://localhost:1883"
                                Address of the MQTT broker.
  --mqtt.topic="prometheus/#"   MQTT topic to subscribe to
  --mqtt.prefix="prometheus"    MQTT topic prefix to remove when creating
metrics
  --log.level="info"            Only log messages with the given severity or
above. Valid levels: [debug, info, warn, error, fatal]
  --log.format="logger:stderr"  Set the log target and format. Example:
"logger:syslog?appname=bob&local=7" or "logger:stdout?json=true"
```

## Installation

Requires go > 1.9

```
go get -u github.com/inuits/sparkpluggw
```

## How does it work?

sparkpluggw will connect to the MQTT broker at `--mqtt.broker-address` and
listen to the topics specified by `--mqtt.topic`.

By default, it will listen to `prometheus/#`.

The format for the topics is as follow:

(https://s3.amazonaws.com/cirrus-link-com/Sparkplug+Topic+Namespace+and+State+ManagementV2.1+Apendix++Payload+B+format.pdf)

'namespace/group_id/message_type/edge_node_id/[device_id]'

The following sections are parsed into the following labels and attached to metrics:

- sp_topic
- sp_namespace
- sp_group_id
- sp_msgtype
- sp_edge_node_id
- sp_device_id

Currently only numeric metrics are supported.

In addition to published metrics, sparkpluggw will also publish two additional metrics per topic where messages have been received.

sp_total_metrics_pushed  - Total metrics processed for that topic
sp_last_pushed_timestamp - Last timestamp of a message received for that topic

## Security

This project does not support authentication yet but that is planned.

## A note about the prometheus config

If you use `job` and `instance` labels, please refer to the [pushgateway
exporter
documentation](https://github.com/prometheus/pushgateway#about-the-job-and-instance-labels).

TL;DR: you should set `honor_labels: true` in the scrape config.
