FROM golang:1.8.5-jessie

ARG mqtt_ip
ARG mqtt_port
ARG mqtt_topic
ARG client_id
ARG log_level
ARG log_format

ENV mqtt_ip $mqtt_ip
ENV mqtt_port $mqtt_port
ENV mqtt_topic $mqtt_topic
ENV client_id $client_id
ENV log_level $log_level
ENV log_format $log_format

# Download and build the Go application
RUN go get github.com/IHI-Energy-Storage/sparkpluggw

# Go into binary directory
WORKDIR /go/bin

# Run the service
ENTRYPOINT  ./sparkpluggw --mqtt.broker-address=tcp://$mqtt_ip:$mqtt_port --mqtt.client-id=$client_id --mqtt.topic=$mqtt_topic --log.level=$log_level --log.format=$log_format
