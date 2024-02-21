build: build_runner build_go build_java build_docker

build_runner:
	go build -C ./golang -o ../runner ./cmd/runner

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
	cat templates/$(name).json | \
		timeout --preserve-status $(TRIAL_DURATION) \
		docker run \
			--interactive \
			--name $(name) \
			--mount type=volume,source=harness_vol,destination=$(workspace)/volume \
			--env NUM_CLIENTS=$(NUM_CLIENTS) \
			--env SERVER_SLEEP_MILLIS=$(SERVER_SLEEP_MILLIS) \
			harness:1 \
			--log-app-name inside
	docker container rm $(name)