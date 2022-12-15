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

#TODO(jakule): Replace the IP
echo "192.168.1.151 ubuntu.example.com" >> /etc/hosts
echo "192.168.1.151 ssh.example.com" >> /etc/hosts

cd /root/build
mkdir ~/.ssh
./tsh config >> ~/.ssh/config

# hack my config
sed -i -e 's/--cluster=example.com/--cluster=ubuntu/' ~/.ssh/config
sed -i -e 's/Host \*.example.com !ubuntu.example.com/Host *.example.com/' ~/.ssh/config

ssh -v jnyckowski@ubuntu.example.com ls -la
