ARG BASE_IMAGE=shenxn/protonmail-bridge:latest
FROM ${BASE_IMAGE}

COPY init.sh       /usr/local/bin/init.sh
COPY entrypoint.sh /usr/local/bin/entrypoint.sh

RUN chmod +x /usr/local/bin/init.sh /usr/local/bin/entrypoint.sh

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
