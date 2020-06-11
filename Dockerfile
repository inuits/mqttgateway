ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:glibc
LABEL maintainer="Julien Pivotto <roidelapluie@inuits.eu>"

ARG ARCH="amd64"
ARG OS="linux"
COPY .build/${OS}-${ARCH}/mqttgateway /bin/mqttgateway

EXPOSE      7979
USER        nobody
ENTRYPOINT  [ "/bin/mqttgateway" ]
