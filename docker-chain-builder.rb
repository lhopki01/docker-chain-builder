# This file was generated by GoReleaser. DO NOT EDIT.
class DockerChainBuilder < Formula
  desc ""
  homepage ""
  url "https://github.com/lhopki01/docker-chain-builder/releases/download/v1.1.3/docker-chain-builder_1.1.3_Darwin_x86_64.tar.gz"
  version "1.1.3"
  sha256 "47c27365fc4ad271525ab87783287423fec16f1c7a178b0b5a80265d79dbe999"
  
  depends_on "docker"

  def install
    bin.install "docker-chain-builder"
  end
end
