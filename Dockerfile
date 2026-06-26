FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
COPY . .
RUN go build -o stock-portfolio ./cmd/stock-portfolio

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/stock-portfolio .
EXPOSE 8080
CMD ["./stock-portfolio"]
