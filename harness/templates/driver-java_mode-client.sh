java --enable-preview -jar javaecho.jar \
    --network unix --address ./volume/echo.sock \
    --threadType virtual \
    client --numClients "${NUM_CLIENTS}" \
    > volume/output/client-0.stdout 2> volume/output/client-0.stderr &
CMD1_PID=$!
pids[$CMD1_PID]=0

jcmd ${CMD1_PID} \
    JFR.start dumponexit=true filename=volume/output/client.jfr \
    > volume/output/client-1.stdout 2> volume/output/client-1.stderr &
pids[$!]=1

sudo perf stat -p ${CMD1_PID} \
    > volume/output/client-2.stdout 2> volume/output/client-2.stderr &
pids[$!]=2