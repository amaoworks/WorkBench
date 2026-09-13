FROM ubuntu:20.04

ENV DEBIAN_FRONTEND=noninteractive \
    FUTU_OPEND_DIR=/opt/FutuOpenD \
    FUTU_OPEND_DATA=/home/futu/.com.futunn.FutuOpenD

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        tar \
        libssl1.1 \
        libstdc++6 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -r futu && useradd -r -g futu -m -d /home/futu futu \
    && mkdir -p /opt/template /opt/FutuOpenD /home/futu/.com.futunn.FutuOpenD \
    && chown -R futu:futu /opt/FutuOpenD /home/futu

COPY FutuOpenD.xml.template /opt/template/FutuOpenD.xml.template
COPY start.sh /usr/local/bin/start-opend
RUN chmod 0755 /usr/local/bin/start-opend

USER futu
WORKDIR /home/futu
EXPOSE 11111 22222
HEALTHCHECK --interval=30s --timeout=5s --start-period=90s --retries=5 \
    CMD bash -c "echo >/dev/tcp/127.0.0.1/11111"

CMD ["/usr/local/bin/start-opend"]
