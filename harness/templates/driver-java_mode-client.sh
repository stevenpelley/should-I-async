java --enable-preview -jar javaecho.jar --network unix \
    --address ./volume/echo.sock client --numClients "${NUM_CLIENTS}" \
    > volume/output/client-0.stdout 2> volume/output/client-0.stderr &
CMD1_PID=$!
pids[$CMD1_PID]=0

jcmd ${CMD1_PID} help \
    > volume/output/client-1.stdout 2> volume/output/client-1.stderr &
pids[$!]=1