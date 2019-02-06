FROM golang:1.8.5-jessie

ARG mqtt_ip
ARG mqtt_port
ARG mqtt_topic
ARG client_id

# Download and build the Go application
RUN go get github.com/IHI-Energy-Storage/sparkpluggw

# Go into binary directory
WORKDIR /go/bin

# Run the service
ENTRYPOINT  [ "./sparkpluggw" ]
CMD         ["--mqtt.broker-address=tcp://$mqtt_ip:$mqtt_port --mqtt.client-id=$client_id --mqtt.topic=$mqtt_topic"]
