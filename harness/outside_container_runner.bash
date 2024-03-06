#!/usr/bin/env bash
# example usage: TRIAL_DURATION=1 NUM_CLIENTS=1 SERVER_SLEEP_MILLIS=0 bash outside_container_runner.bash templates/driver-java_mode-server.sh templates/driver-java_mode-client.sh
if [ $# -ne 2 ]; then
  echo "require 2 argments, server template and client template files" >&2
  exit 1
fi
# these must be relative to to the harness directory as we bind mount that in
# docker
server_template=$1;
client_template=$2;

# ENV VARIABLES
[ -n "$TRIAL_DURATION" ] || { echo "require TRIAL_DURATION" >&2; exit 1; }
[ -n "$NUM_CLIENTS" ] || { echo "require NUM_CLIENTS" >&2; exit 1; }
[ -n "$SERVER_SLEEP_MILLIS" ] || { echo "require SERVER_SLEEP_MILLIS" >&2; exit 1; }

con_workspace=/workspace

docker volume create harness_vol

# start the server
server_con_id=$(\
    docker run \
        --detach \
        --name server \
        --mount type=volume,source=harness_vol,destination="${con_workspace}"/volume \
        --mount type=bind,source="$(pwd)"/templates,destination="${con_workspace}"/templates,readonly \
        --env NUM_CLIENTS="${NUM_CLIENTS}" \
        --env SERVER_SLEEP_MILLIS="${SERVER_SLEEP_MILLIS}" \
        harness:1 \
        "${server_template}" \
    )

# run the client synchronously
timeout --preserve-status ${TRIAL_DURATION} \
    docker run \
        --name client \
        --mount type=volume,source=harness_vol,destination="${con_workspace}"/volume \
        --mount type=bind,source="$(pwd)"/templates,destination="${con_workspace}"/templates,readonly \
        --env NUM_CLIENTS="${NUM_CLIENTS}" \
        --env SERVER_SLEEP_MILLIS="${SERVER_SLEEP_MILLIS}" \
        harness:1 \
        "${client_template}"
client_con_id=$(docker container ls -lq)

docker stop ${server_con_id}
docker cp ${client_con_id}:/workspace/volume/output output/
docker container rm ${server_con_id}
docker container rm ${client_con_id}
docker volume rm harness_vol