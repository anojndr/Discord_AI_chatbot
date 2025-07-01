# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install build dependencies for CGO
RUN apk add --no-cache gcc musl-dev sqlite-dev

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application with CGO enabled
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o bin/bot cmd/bot/main.go

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add \
    ca-certificates \
    python3 \
    python3-dev \
    py3-pip \
    py3-virtualenv \
    gcc \
    musl-dev \
    linux-headers \
    && rm -rf /var/cache/apk/*

WORKDIR /app

# Create Python virtual environment for chart generation
RUN python3 -m venv /app/chart_venv && \
    /app/chart_venv/bin/pip install --no-cache-dir \
    matplotlib \
    pandas \
    numpy \
    plotly \
    seaborn

# Copy the binary from builder stage
COPY --from=builder /app/bin/bot .

# Copy config files
COPY configs/ ./configs/

# Set environment variables
ENV PATH="/app/chart_venv/bin:$PATH"
ENV PYTHONPATH="/app/chart_venv/lib/python3.12/site-packages"

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

CMD ["./bot"] 