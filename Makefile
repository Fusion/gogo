BINARY_NAME := gogo
PLATFORMS := darwin/arm64 linux/amd64

all: $(PLATFORMS)

$(PLATFORMS):
	GOOS=$(word 1,$(subst /, ,$@)) GOARCH=$(word 2,$(subst /, ,$@)) go build -ldflags="-s -w" -o $(BINARY_NAME)-$(subst /,-,$@) main.go

clean:
	rm -f $(BINARY_NAME)-darwin-arm64 $(BINARY_NAME)-linux-amd64

.PHONY: all clean $(PLATFORMS)
