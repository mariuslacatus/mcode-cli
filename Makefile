.PHONY: build run test clean

BUILD_VERSION_FILE := .build_version

build:
	@version=$$( \
		if [ ! -f $(BUILD_VERSION_FILE) ]; then echo 0 > $(BUILD_VERSION_FILE); fi; \
		expr $$(cat $(BUILD_VERSION_FILE)) + 1 \
	); \
	echo $$version > $(BUILD_VERSION_FILE); \
	echo "Building MCode version v1.0.$$version..."; \
	go build -ldflags "-X main.BuildVersion=v1.0.$$version" -o mcode

run: build
	./mcode

test:
	go test ./...

clean:
	rm -f mcode
