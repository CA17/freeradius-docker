FROM freeradius/freeradius-server:latest

RUN apt-get update
RUN apt install -y net-tools vim tcpdump iproute2 \
    && apt-get clean \
    && rm -r /var/lib/apt/lists/*

COPY freeradius/ /etc/raddb/
