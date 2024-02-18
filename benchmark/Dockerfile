# build grpc and http benchmark clients
FROM golang:1.22 as build
WORKDIR /src
ENV CGO_ENABLED=1

RUN go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o grpc_bench_client ./benchmark/grpc/grpc_bench.go; \
go build -o http_bench_client ./benchmark/http/http_bench.go


FROM debian:12-slim
RUN apt update -y; apt install curl wget htop -y
COPY --from=build /src/*_bench_client /usr/bin/
COPY --from=build /go/bin/grpcurl /usr/bin/

CMD ["tail", "-f", "/dev/null"]