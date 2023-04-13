FROM python:3.11

RUN mkdir /ai_service
COPY /ai_service /ai_service
COPY pyproject.toml /ai_service
COPY pyproject.toml /ai_service

WORKDIR /ai_service

ENV PYTHONPATH=${PYTHONPATH}:${PWD}

RUN pip3 install poetry
RUN poetry config virtualenvs.create false
RUN poetry install --no-dev

ENV FLASK_APP="ai_service/app.py"

CMD ["flask", "run"]