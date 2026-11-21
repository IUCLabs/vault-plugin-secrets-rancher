FROM scratch
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/vault-plugin-secrets-rancher /vault-plugin-secrets-rancher
ENTRYPOINT ["/vault-plugin-secrets-rancher"]
