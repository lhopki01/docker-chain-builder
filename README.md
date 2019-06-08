## docker-chain-builder

docker-chain-builder is a tool to build and push chains of dependent docker images.
First a dependency graph is created and then docker-chain-builder walks the graph updating the VERSION files and the FROM lines in the Dockerfile.
All versions are in semver.
Individual docker images that are not part of a chain can be built too.
If fed a list of images docker-chain-builder will figure out which images are the start of chains and build all the chains simultaneously.

## Installation

Install docker
`brew install docker`
Install docker-chain-builder
`go get github.com/lhopki01/docker-chain-builder`

## Setup

All Dockerfiles should be in separate folders named after the docker repository (not registry)
Create a VERSION file in each folder with an initial version.

Create a file called conf.yaml in the folder containing all the Dockerfile folders.
Put `registry: name-of-regisry` in it.   All images will be pushed here. E.g.
```
test_dirs
├── conf.yaml
├── alpha
│   ├── Dockerfile
│   └── VERSION
├── alpha-2
│   ├── Dockerfile
│   └── VERSION
├── alpha-2-beta
│   ├── Dockerfile
│   └── VERSION
├── alpha-1
│   ├── Dockerfile
│   └── VERSION
├── charlie
│   ├── Dockerfile
│   └── VERSION
└── charlie-1
    ├── Dockerfile
    └── VERSION
```

## Usage

### Dry-run
`docker-chain-builder build [path/to/dockerfilefolder] --bump [major,minor,patch,pre,none] -n`

### Build
`docker-chain-builder build [path/to/dockerfilefolder] --bump [major,minor,patch,pre,none]`

### Build and push
`docker-chain-builder build [path/to/dockerfilefolder] --bump [major,minor,patch,pre,none] --push`

### Build multiple images
`docker-chain-builder build alpha charlie alpha-2 --bump patch`
