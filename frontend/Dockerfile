FROM node:20.18-alpine3.20 AS builder
WORKDIR /app
COPY package.json ./
RUN npm install --no-audit --no-fund
COPY . .

# API adresi derleme anında gömülür (Vite import.meta.env).
#
# Varsayılan GÖRELİ (/api/v1): arayüz kendi kökenine istek atar, nginx bunu
# compose ağındaki backend'e vekiller (bkz. nginx.conf). Böylece IP/hostname
# değişse bile yeniden derleme gerekmez ve telefondan erişim çalışır.
#
# Mutlak adres verirseniz (ARG VITE_API_URL=http://host:8080/api/v1) doğrudan
# backend'e gider; o durumda CORS ve IP sabitleme sorunları geri gelir.
ARG VITE_API_URL=/api/v1
ENV VITE_API_URL=$VITE_API_URL

RUN npm run build

FROM nginxinc/nginx-unprivileged:1.27-alpine3.20
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]