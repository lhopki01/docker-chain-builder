#!/bin/bash

set -euo pipefail

rm -rf test_dirs

mkdir test_dirs
echo "registry: docker/registry" > test_dirs/conf.yaml

mkdir test_dirs/alpha
echo "FROM alpine:3.9" > test_dirs/alpha/Dockerfile
echo "RUN sleep 1" >> test_dirs/alpha/Dockerfile
echo "1.0.0" > test_dirs/alpha/VERSION

mkdir test_dirs/alpha-2
echo "FROM docker/registry/alpha:1.0.0" > test_dirs/alpha-2/Dockerfile
echo "RUN sleep 1" >> test_dirs/alpha-2/Dockerfile
echo "0.1.0" > test_dirs/alpha-2/VERSION

mkdir test_dirs/alpha-1
echo "FROM docker/registry/alpha:1.0.0" > test_dirs/alpha-1/Dockerfile
echo "RUN sleep 1" >> test_dirs/alpha-1/Dockerfile
echo "0.1.0" > test_dirs/alpha-1/VERSION

mkdir test_dirs/alpha-1-beta
echo "FROM docker/registry/alpha-1:0.1.0" > test_dirs/alpha-1-beta/Dockerfile
echo "RUN sleep 1" >> test_dirs/alpha-1-beta/Dockerfile
echo "0.0.1" > test_dirs/alpha-1-beta/VERSION

mkdir test_dirs/charlie
echo "FROM alpine:3.9" > test_dirs/charlie/Dockerfile
echo "RUN sleep 1" >> test_dirs/charlie/Dockerfile
echo "1.0.0" > test_dirs/charlie/VERSION

mkdir test_dirs/charlie-1
echo "FROM docker/registry/charlie:1.0.0" > test_dirs/charlie-1/Dockerfile
echo "RUN sleep 1" >> test_dirs/charlie-1/Dockerfile
echo "0.1.0" > test_dirs/charlie-1/VERSION
