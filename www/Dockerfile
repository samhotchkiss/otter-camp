# Simple nginx static file server for sales site
FROM nginx:alpine

# Copy static files
COPY index.html /usr/share/nginx/html/
COPY indeed.html /usr/share/nginx/html/
COPY images/ /usr/share/nginx/html/images/
COPY personas/ /usr/share/nginx/html/personas/

# Custom nginx config for SPA fallback and port
RUN echo 'server { \
    listen 8080; \
    root /usr/share/nginx/html; \
    index index.html; \
    location / { \
        try_files $uri $uri/ /index.html; \
    } \
}' > /etc/nginx/conf.d/default.conf

EXPOSE 8080

CMD ["nginx", "-g", "daemon off;"]
