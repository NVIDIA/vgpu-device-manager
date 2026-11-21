# Copyright (c) 2022, NVIDIA CORPORATION.  All rights reserved.
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
FROM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf

WORKDIR /work

RUN go install golang.org/x/lint/golint@latest
RUN go install github.com/matryer/moq@latest
RUN go install github.com/gordonklaus/ineffassign@latest
RUN go install github.com/client9/misspell/cmd/misspell@latest

# We need to set the /work directory as a safe directory.
# This allows git commands to run in the container.
RUN git config --file=/.gitconfig --add safe.directory /work
