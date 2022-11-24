FROM golang:1.19.3-alpine3.16 as builder

WORKDIR /workspace
RUN apk add --no-cache git make
COPY . .

RUN make

################################################################################

FROM centos:centos7
LABEL maintainers="Kubernetes Authors"
LABEL description="Image Driver"
LABEL source_repository="https://github.com/sapcc/csi-driver-image-extractor"

RUN \
  yum install -y epel-release && \
  yum install -y skopeo && \
  yum clean all

COPY --from=builder /workspace/bin/image-extractor-plugin /image-extractor-plugin
ENTRYPOINT ["/image-extractor-plugin"]

