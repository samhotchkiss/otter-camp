#!/bin/sh
# Substitute only PORT and API_BACKEND_URL in the nginx template
envsubst '$PORT $API_BACKEND_URL' < /etc/nginx/templates/default.conf.template > /etc/nginx/conf.d/default.conf
exec nginx -g 'daemon off;'
