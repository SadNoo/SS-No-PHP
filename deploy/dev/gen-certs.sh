#!/bin/sh
# 生成本地开发用的 MySQL TLS 证书（带 SAN，可通过严格校验）。仅用于开发，切勿用于生产。
set -eu
cd "$(dirname "$0")"
if [ -f certs/server-cert.pem ]; then echo "证书已存在，跳过生成"; exit 0; fi
mkdir -p certs && cd certs
openssl req -x509 -newkey rsa:2048 -nodes -keyout ca-key.pem -out ca.pem -days 825 -subj "/CN=niuniu-dev-ca"
openssl req -newkey rsa:2048 -nodes -keyout server-key.pem -out server.csr -subj "/CN=localhost"
printf "subjectAltName=DNS:localhost,IP:127.0.0.1\n" > ext.cnf
openssl x509 -req -in server.csr -CA ca.pem -CAkey ca-key.pem -CAcreateserial -out server-cert.pem -days 825 -extfile ext.cnf
chmod 644 ./*.pem
echo "证书已生成到 $(pwd)，配置 database.tls_ca = \"$(pwd)/ca.pem\""
