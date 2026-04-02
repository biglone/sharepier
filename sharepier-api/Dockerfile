FROM golang:1.26.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/sharepier-api ./cmd/api

FROM alpine:3.22
WORKDIR /app
COPY --from=build /out/sharepier-api /app/sharepier-api
EXPOSE 8080
ENTRYPOINT ["/app/sharepier-api"]
