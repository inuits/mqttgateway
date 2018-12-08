# iron/go:dev is the alpine image with the go tools added
FROM iron/go:dev




WORKDIR /mqttgateway

ENV SRC_DIR=/go/src/github.com/IHI-Energy-Storage/mqttgateway/
EXPOSE 9337

ADD . $SRC_DIR

# Get dependencies
RUN go get github.com/eclipse/paho.mqtt.golang
RUN go get github.com/golang/protobuf/proto
RUN go get github.com/prometheus/client_golang/prometheus
RUN go get github.com/prometheus/client_golang/prometheus/promhttp
RUN go get github.com/prometheus/common/log
RUN go get github.com/prometheus/common/log

# Build it:
RUN cd $SRC_DIR; go build -o mqttgateway; cp mqttgateway /mqttgateway/

ENTRYPOINT ["./mqttgateway --mqtt.broker-address=tcp://172.30.20.67:1883 --mqtt.client-id=MQTT_FX_Client --mqtt.format=sparkplug --mqtt.topic=spBv1.0/prod:ess/#"]
