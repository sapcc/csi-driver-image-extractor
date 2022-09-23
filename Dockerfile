FROM centos:centos7
LABEL maintainers="Kubernetes Authors"
LABEL description="Image Driver"

RUN \
  yum install -y epel-release && \
  yum install -y skopeo && \
  yum clean all

COPY ./bin/image-extractor-plugin /image-extractor-plugin
ENTRYPOINT ["/image-extractor-plugin"]

