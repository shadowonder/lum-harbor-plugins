.PHONY: all build build-windows test clean

ARTIFACT_DIR := dist/lh-plugin-base
EXECUTABLE := $(ARTIFACT_DIR)/lh-plugin-base

ARTIFACT_DIR_WINDOWS := dist/lh-plugin-base-windows-amd64
EXECUTABLE_WINDOWS := $(ARTIFACT_DIR_WINDOWS)/lh-plugin-base.exe

all: build

build:
	mkdir -p $(ARTIFACT_DIR)
	go build -trimpath -o $(EXECUTABLE) ./cmd/example-plugin
	cp cmd/example-plugin/plugin.json $(ARTIFACT_DIR)/plugin.json

# build-windows produces a normal Plugin artifact directory (plugin.json +
# matching executable) for windows/amd64. The manifest executable field is
# artifact truth, not host-side filename inference, so plugin.json is
# patched to name the real Windows binary rather than shipping the Unix
# manifest unchanged next to a .exe it doesn't mention.
build-windows:
	mkdir -p $(ARTIFACT_DIR_WINDOWS)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o $(EXECUTABLE_WINDOWS) ./cmd/example-plugin
	sed 's/"executable": "lh-plugin-base"/"executable": "lh-plugin-base.exe"/' cmd/example-plugin/plugin.json > $(ARTIFACT_DIR_WINDOWS)/plugin.json

test:
	go test ./...

clean:
	rm -rf dist
	rm -f lh-plugin-base
