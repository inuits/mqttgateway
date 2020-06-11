DOCKER_ARCHS ?= amd64 armv7 arm64 ppc64le s390x

include Makefile.common

DOCKER_IMAGE_NAME       ?= mqttgateway
MACH                    ?= $(shell uname -m)
