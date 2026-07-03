# HUAKAI frontend (Next.js) production image.
# Runs the Next.js Node server (`next start`) so runtime rewrites() in
# next.config.mjs can proxy /v1 /admin/v1 /debug to the gateway. The gateway URL
# is injected at runtime via HUAKAI_GATEWAY_URL (defaults to http://gateway:8080
# on the compose network). Build context is the frontend/ directory.

# ---- builder ----
FROM node:22-alpine AS builder
WORKDIR /app

# Install deps against the committed lockfile for reproducible builds.
COPY package.json package-lock.json ./
RUN npm ci

# Build the app.
COPY . .
RUN npm run build

# ---- runner ----
FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV PORT=3000
# Default gateway target on the compose network; override per deployment.
ENV HUAKAI_GATEWAY_URL=http://gateway:8080

# Non-root runtime user.
RUN addgroup -g 10001 -S huakai && adduser -u 10001 -S huakai -G huakai

# Copy the built application and its runtime dependencies.
COPY --from=builder /app/package.json /app/package-lock.json ./
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/.next ./.next
COPY --from=builder /app/public ./public
COPY --from=builder /app/next.config.mjs ./next.config.mjs

USER huakai
EXPOSE 3000
# next start binds 0.0.0.0:$PORT.
CMD ["npm", "run", "start"]
