FROM python:3.12-slim
WORKDIR /srv
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY app ./app
COPY loadgen.py ./loadgen.py
ENV PYTHONUNBUFFERED=1
EXPOSE 18002
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "18002"]
