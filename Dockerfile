# We expect the serving table to be in /workspace/index
FROM gcr.io/jonjohnson-test/kythe/bins as bins
FROM gcr.io/jonjohnson-test/kythe/dist as dist

# Final image, serves the index via web ui
FROM gcr.io/distroless/cc:debug
COPY --from=dist /src/kythe/web/ui/resources/public /public
COPY --from=bins /out/http_server /http_server
COPY /workspace/index /index
EXPOSE 8080
CMD ["/http_server", "--listen", ":8080", "--public_resources", "/public", "--serving_table", "/index"]
