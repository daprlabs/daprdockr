FROM base/devel
MAINTAINER Reuben Bond, reuben.bond@gmail.com
RUN yaourt --noconfirm -Syy extra/sqlite community/nginx
ADD daprdockrd daprdockrd
#RUN useradd service -d /data/downloads -r
#ADD supervisord.conf /etc/supervisord.conf
VOLUME ["/host/docker", "/host/proc"]

#USER service
EXPOSE 80 433
CMD /daprdockrd -route /host/proc/net/route -docker unix:///host/docker/docker.sock -etcd $ETCD_HOSTS