MODULE := github.com/lhopki01/docker-chain-builder

lint:
	golangci-lint run

release:
	git tag -a $$VERSION
	git push origin $$VERSION
	goreleaser --rm-dist

build:
	CGO_ENABLED=0 go build -ldflags "-X $(MODULE)/cmd.Version=$$VERSION"
