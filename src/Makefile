BINARY = cuento-cli
DIST = $(CURDIR)/dist

.PHONY: all mac windows clean

all: mac windows

mac:
	mkdir -p $(DIST)
	cd v1 && GOOS=darwin  GOARCH=amd64 go build -o $(DIST)/$(BINARY)-mac .

windows:
	mkdir -p $(DIST)
	cd v1 && GOOS=windows GOARCH=amd64 go build -o $(DIST)/$(BINARY).exe .

clean:
	rm -rf $(DIST)
