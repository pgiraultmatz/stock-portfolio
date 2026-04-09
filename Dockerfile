FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY . .
RUN go build -o stock-portfolio .

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/stock-portfolio .
EXPOSE 8080
CMD ["./stock-portfolio"]
