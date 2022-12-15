#!/bin/bash
#
# Copyright 2022 Gravitational, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

set -e

# Start Teleport Proxy+Auth with session_recording: "proxy" before running this script.

# Create OpenSSH keys
tctl auth export --type=user | sed s/cert-authority\ // > teleport_user_ca.pub
tctl auth sign --overwrite --host=example.com,ssh.example.com,127.0.0.1 --format=openssh --out=example.com

# Server test - sshd

# Test CentOS 7 release. CentOS 8 doesn't work anymore as all public repos are down.
docker build -f Dockerfile-centos7 -t openssh-centos7 .
docker run -p3082:22 openssh-centos7:latest &
sleep 3
tsh ssh -p 3082 root@ssh.example.com ls -la
docker ps | grep openssh-centos7 | awk '{print $1}' | xargs docker stop

# Test Ubuntu releases.
for ver in '18.04' '20.04' '22.04' '22.10';
do
  docker build --build-arg OS_VERSION=${ver} -f Dockerfile-ubuntu -t openssh-ubuntu:${ver} .
  docker run -p3082:22 openssh-ubuntu:${ver} &
  sleep 3
  tsh ssh -p 3082 root@ssh.example.com ls -la
  docker ps | grep openssh- | awk '{print $1}' | xargs docker stop
done


# Client test- ssh

cp client.sh ../../build

docker run -p3082:22 -v"${HOME}"/.tsh:/root/.tsh -v"$(pwd)/../../build":/root/build openssh-centos7:latest /bin/bash -c /root/build/client.sh

for ver in '18.04' '20.04' '22.04' '22.10';
do
  docker run -it -p3082:22 -v"${HOME}"/.tsh:/root/.tsh -v"$(pwd)/../../build":/root/build openssh-ubuntu:${ver} /bin/bash -c /root/build/client.sh
done