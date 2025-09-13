BINARY_NAME := gogo
PLATFORMS := darwin/arm64 linux/amd64

all: $(PLATFORMS)

$(PLATFORMS):
	GOOS=$(word 1,$(subst /, ,$@)) GOARCH=$(word 2,$(subst /, ,$@)) go build -ldflags="-s -w" -o $(BINARY_NAME)-$(subst /,-,$@) main.go

package:
	cp sampleconfig/config.toml . \
	&& cp templates/config.toml sampleconfig/config.toml \
	&& tar -z -c -v --format ustar -f config.tgz sampleconfig \
	&& cp config.toml sampleconfig/config.toml

clean:
	rm -f $(BINARY_NAME)-darwin-arm64 $(BINARY_NAME)-linux-amd64

.PHONY: all package clean $(PLATFORMS)
