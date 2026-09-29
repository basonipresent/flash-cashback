FROM node:20-alpine
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install
COPY . .
EXPOSE 8081 19000
ENTRYPOINT ["npx", "expo", "start", "--web", "--port", "8081"]
