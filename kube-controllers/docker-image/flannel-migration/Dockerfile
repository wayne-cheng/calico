# Copyright 2019 Tigera, Inc
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

ARG CALICO_BASE

FROM alpine:3 AS builder

# Make sure the status file is owned by our user.
RUN mkdir /status
RUN touch /status/status.json && chown 999 /status/status.json

FROM scratch AS source

ARG BIN_DIR
ARG TARGETARCH

COPY --from=builder /status /status/

COPY ${BIN_DIR}/LICENSE /licenses/LICENSE

COPY ${BIN_DIR}/kubectl-${TARGETARCH} /usr/bin/kubectl
COPY ${BIN_DIR}/kube-controllers-linux-${TARGETARCH} /usr/bin/kube-controllers
COPY ${BIN_DIR}/check-status-linux-${TARGETARCH} /usr/bin/check-status

FROM ${CALICO_BASE}

ARG GIT_VERSION=unknown

LABEL org.opencontainers.image.description="Calico Flannel migration controller updates a flannel cluster to Calico"
LABEL org.opencontainers.image.authors="maintainers@tigera.io"
LABEL org.opencontainers.image.source="https://github.com/projectcalico/calico"
LABEL org.opencontainers.image.title="Calico Flannel migration controller"
LABEL org.opencontainers.image.vendor="Project Calico"
LABEL org.opencontainers.image.version="${GIT_VERSION}"
LABEL org.opencontainers.image.licenses="Apache-2.0"

COPY --from=source / /

USER 999

ENTRYPOINT ["/usr/bin/kube-controllers"]
