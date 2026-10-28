FROM alpine:3.22
COPY generator-target /target
ENV GENERATOR_LAB=synthetic-only
USER 65534:65534
ENTRYPOINT ["/target"]
