
FROM debian:bullseye-20231218 as WAITPID
RUN apt-get update
RUN apt-get install -y git autopoint autoconf gettext flex bison libtool make
WORKDIR /linux-util-build
RUN git clone --depth 1 --branch stable/v2.40 https://github.com/util-linux/util-linux.git
WORKDIR /linux-util-build/util-linux
RUN ./autogen.sh
RUN ./configure --disable-all-programs --enable-waitpid || cat config.log
RUN make

WORKDIR /waitn
RUN git clone https://github.com/stevenpelley/waitn

FROM debian:bullseye-20231218
ENV JAVA_HOME=/opt/java/openjdk
COPY --from=eclipse-temurin:21.0.1_12-jdk $JAVA_HOME $JAVA_HOME
ENV PATH="${JAVA_HOME}/bin:${PATH}"

RUN rm /bin/sh && ln -s /bin/bash /bin/sh
WORKDIR /workspace
RUN apt update
RUN apt install -y linux-perf
RUN ln -s /usr/bin/perf_5.10 /usr/bin/perf_6.6

COPY javaecho.jar .
COPY goecho /workspace/

COPY --from=WAITPID /linux-util-build/util-linux/waitpid .
COPY --from=WAITPID /waitn/waitn/wait.bash .
COPY wait_waitpid.bash .
COPY inside_container_runner.bash .

ENTRYPOINT ["/bin/bash", "inside_container_runner.bash"]

#ENTRYPOINT ["./runner"]
#ENTRYPOINT ["java",  "--enable-preview",  "-Djava.net.preferIPv4Stack=true", \
#    "-XX:FlightRecorderOptions=stackdepth=128", \
#    "-XX:+UnlockDiagnosticVMOptions", \
#    "-XX:+DebugNonSafepoints", \
#    "-XX:StartFlightRecording=delay=1s,duration=3s,name=StartFlightRecorder,filename=/mnt/mine/flight-recording.jfr", \
#    "-jar", "app.jar"]

#ENTRYPOINT ["java",  "--enable-preview", "-jar", "app.jar"]

#RUN apt-get update
#RUN apt-get install -y curl unzip zip
#ENV SDKMAN_DIR="/workspace/sdkman"
#RUN curl -s "https://get.sdkman.io?rcupdate=false" | bash
##RUN curl -s "https://get.sdkman.io" | bash
#RUN set -x \
#    && echo "sdkman_auto_answer=true" > $SDKMAN_DIR/etc/config \
#    && echo "sdkman_auto_selfupdate=false" >> $SDKMAN_DIR/etc/config \
#    && echo "sdkman_insecure_ssl=false" >> $SDKMAN_DIR/etc/config
#RUN [[ -s "$SDKMAN_DIR/bin/sdkman-init.sh" ]] && source "$SDKMAN_DIR/bin/sdkman-init.sh" && exec "$@"
#RUN source "$SDKMAN_DIR/bin/sdkman-init.sh" && sdk install java 21.0.1-oracle
#