build: build_runner build_go build_java build_docker

build_runner:
	go build -C ../golang -o ../harness/runner ./cmd/runner

build_go:
	go build -C ../golang -o ../harness/goecho ./cmd/tcpecho

build_java:
	gradle -p ../javaecho build
	cp ../javaecho/app/build/libs/app.jar ./javaecho.jar

clean_docker:
	docker container prune
	docker image prune
	docker volume prune

build_docker:
	docker build . --file ./Dockerfile -t harness:1
	docker volume create harness_vol

# e.g., make run_java_client
run_%: templates/%.json
	$(eval workspace := /workspace)
	$(eval name := $*)
	docker run \
		--name $(name) \
		--mount type=volume,source=harness_vol,destination=$(workspace)/volume \
		--mount type=bind,source=./templates/,destination=$(workspace)/templates,readonly \
		harness:1 \
		--input $(workspace)/templates/$(name).json \
		--outputDir $(workspace)/volume/output/$(name) \
		--numClients=$(NUMCLIENTS) \
		--serverSleepDurationMillis=$(SERVERSLEEPMILLIS) \
		--testDuration=$(TESTDURATION)
	docker cp $(name):$(workspace)/volume/output/$(name) output_$(name)
	docker container rm $(name)