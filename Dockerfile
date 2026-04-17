FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /server main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /
COPY --from=builder /server /server
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/server"]
