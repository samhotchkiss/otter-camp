# Dockerfile - Frontend (Multi-stage build)
# Builds React/Vite app and serves with nginx

# =============================================================================
# Stage 1: Build
# =============================================================================
FROM node:20-alpine AS builder

WORKDIR /app

# Copy package files
COPY package*.json ./

# Install dependencies
RUN npm ci

# Copy source
COPY . .

# Build args for environment
ARG VITE_API_URL=https://api.otter.camp
ENV VITE_API_URL=$VITE_API_URL

# Build the app
RUN npm run build

# =============================================================================
# Stage 2: Serve
# =============================================================================
FROM nginx:alpine

# Copy built assets
COPY --from=builder /app/dist /usr/share/nginx/html

# Copy nginx config for SPA routing
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
