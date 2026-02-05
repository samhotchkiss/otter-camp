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

# Build the app (skip tsc, use vite directly)
RUN npx vite build

# =============================================================================
# Stage 2: Serve
# =============================================================================
FROM nginx:alpine

# Copy built assets
COPY --from=builder /app/dist /usr/share/nginx/html

# Copy nginx config template
COPY nginx.conf /etc/nginx/templates/default.conf.template

# Railway provides PORT env var, default to 80 for local
ENV PORT=80

EXPOSE ${PORT}

CMD ["nginx", "-g", "daemon off;"]
