build: build_runner build_go build_java

build_runner:
	go build -C ../golang -o ../harness/runner ./cmd/runner

build_go:
	go build -C ../golang -o ../harness/goecho ./cmd/tcpecho

build_java:
	gradle -p ../javaecho build
	cp ../javaecho/app/build/libs/app.jar ./javaecho.jar

run_%: templates/%.json
	mkdir -p output/$*
	./runner --input $< --outputDir output/$* --numClients=$(NUMCLIENTS) --serverSleepDurationMillis=$(SERVERSLEEPMILLIS) --testDuration=$(TESTDURATION)