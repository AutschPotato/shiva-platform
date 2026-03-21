FROM grafana/k6:latest

USER root

RUN apk add --no-cache curl

COPY k6-scripts/entrypoint.sh /usr/local/bin/shiva-k6-entrypoint.sh

# Normalize Windows CRLF line endings inside the image so Docker Desktop
# checkouts do not break the worker startup shell parsing.
RUN sed -i 's/\r$//' /usr/local/bin/shiva-k6-entrypoint.sh \
  && mkdir -p /scripts /output \
  && chmod 0777 /scripts /output \
  && chmod +x /usr/local/bin/shiva-k6-entrypoint.sh

USER 12345

ENTRYPOINT ["/bin/sh", "/usr/local/bin/shiva-k6-entrypoint.sh"]
