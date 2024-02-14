FROM golang:1.22-alpine as build
WORKDIR /src
RUN apk add --no-cache build-base
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN make build


FROM alpine:3.19
RUN apk add --no-cache --update \
  && apk add --no-cache curl redis

COPY --from=build /src/election-agent /app/

CMD ["/app/election-agent"]
EXPOSE 80 443
