java --enable-preview -jar javaecho.jar --network unix \
    --address ./volume/echo.sock server --sleepDuration "${SERVER_SLEEP_MILLIS}" \
    > volume/output/server-0.stdout 2> volume/output/server-0.stderr &
CMD1_PID=$!
pids[$CMD1_PID]=0

bash -c "echo $CMD1_PID" \
    > volume/output/server-1.stdout 2> volume/output/server-1.stderr &
pids[$!]=1