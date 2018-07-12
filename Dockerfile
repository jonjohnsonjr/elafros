# In knative
FROM gcr.io/jonjohnson-test/kythe/bins as bins
FROM gcr.io/jonjohnson-test/kythe/dist as dist
FROM gcr.io/jonjohnson-test/kythe/indexer as indexer

# Final image, serves the index via web ui
FROM ubuntu
COPY --from=dist /src/kythe/web/ui/resources/public /public
COPY --from=bins /out/http_server /http_server
COPY --from=indexer /tmp/out /index
EXPOSE 8080
CMD ["/http_server", "--listen", ":8080", "--public_resources", "/public", "--serving_table", "/index"]
