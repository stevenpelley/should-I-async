SHELL=/bin/bash

build: build_container1 build_go build_java build_docker

build_container1:
	go build -C ./golang -o ../container1 ./cmd/container1

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
	docker build \
		--progress=plain \
		--file ./Dockerfile \
		-t harness:1 \
		. \
		2>&1 | tee docker_build.log

build_docker_nocache:
	docker build \
		--no-cache \
		--progress=plain \
		--file Dockerfile \
		-t harness:1 \
		. \
		2>&1 | tee docker_build.log

# e.g., make run_java_client
run_%: templates/%.sh
	$(eval workspace := /workspace)
	$(eval name := $*)
	$(eval template_file := $<)
	timeout --preserve-status $(TRIAL_DURATION) \
	docker run \
		--name $(name) \
		--mount type=volume,source=harness_vol,destination=$(workspace)/volume \
		--mount type=bind,source=$(PWD)/templates,destination=$(workspace)/templates,readonly \
		--env NUM_CLIENTS=$(NUM_CLIENTS) \
		--env SERVER_SLEEP_MILLIS=$(SERVER_SLEEP_MILLIS) \
		harness:1 \
		$(template_file)
	mkdir -p output/
	container_id=$$(docker container ls -lq); \
		docker cp $${container_id}:/workspace/volume/output output/$${container_id}
	docker container rm $(name)