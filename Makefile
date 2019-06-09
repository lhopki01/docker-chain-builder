lint:
	golangci-lint run

release:
	git tag -a $$VERSION
	git push origin $$VERSION
	goreleaser --rm-dist

