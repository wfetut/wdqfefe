

start-db:
	mkdir -p qdrant
	docker run -p 6333:6333 -p 6334:6334 \
        -v $(pwd)/qdrant/storage:/qdrant/storage \
        qdrant/qdrant

start-app:
	flask --app ai_service/app.py run


ingest:
	poetry run python ai_service/ingest.py