# Builder phase.
FROM golang:1.22.3 AS builder
ADD . /usr/src/k8s-dummy-device-plugin
WORKDIR /usr/src/k8s-dummy-device-plugin
# RUN go build dummy.go
RUN CGO_ENABLED=0 go build -a -o k8s-dummy-device-plugin dummy.go

# Copy phase
FROM alpine:latest
# If you need to debug, add bash.
# RUN apk add --no-cache bash
COPY --from=builder /usr/src/k8s-dummy-device-plugin/k8s-dummy-device-plugin /k8s-dummy-device-plugin
COPY --from=builder /usr/src/k8s-dummy-device-plugin/dummyResources.json /dummyResources.json
ENTRYPOINT ["/k8s-dummy-device-plugin"]
