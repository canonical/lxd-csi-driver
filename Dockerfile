FROM scratch
COPY lxd-csi /bin/lxd-csi
ENTRYPOINT ["/bin/lxd-csi"]
