FROM alpine:3.22

RUN apk add --no-cache findmnt

COPY lxd-csi /bin/lxd-csi
RUN chmod +x /bin/lxd-csi

ENTRYPOINT ["/bin/lxd-csi"]
