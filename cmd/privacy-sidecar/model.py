"""Privacy filter model: pure AI-based PII detection."""

from typing import List


class Span:
    """A detected sensitive span."""

    def __init__(self, label: str, text: str, start: int, end: int, score: float):
        self.label = label
        self.text = text
        self.start = start
        self.end = end
        self.score = score

    def to_dict(self) -> dict:
        return {
            "label": self.label,
            "text": self.text,
            "start": self.start,
            "end": self.end,
            "score": self.score,
        }


class PrivacyModel:
    """Loads a HuggingFace NER model and performs PII detection."""

    # Map model labels to our EntityType labels
    _label_map = {
        "PER": "person",
        "PERSON": "person",
        "PHONE": "phone",
        "TEL": "phone",
        "IP": "ip",
        "DOMAIN": "domain",
        "EMAIL": "email",
        "IDCARD": "idcard",
        "ID": "idcard",
        "ADDR": "address",
        "ADDRESS": "address",
        "SECRET": "secret",
        "URL": "url",
        "DATE": "date",
    }

    def __init__(self, model_path: str, device: str = "cpu", max_length: int = 512):
        from transformers import (
            AutoModelForTokenClassification,
            AutoTokenizer,
            pipeline,
        )

        tokenizer = AutoTokenizer.from_pretrained(model_path)
        model = AutoModelForTokenClassification.from_pretrained(model_path)

        self._pipeline = pipeline(
            "ner",
            model=model,
            tokenizer=tokenizer,
            device=device,
            aggregation_strategy="simple",
        )
        self.max_length = max_length
        self.ready = True

    def detect(self, text: str) -> List[Span]:
        if not text:
            return []

        # Truncate if too long
        if len(text) > self.max_length * 3:
            text = text[: self.max_length * 3]

        results = self._pipeline(text)
        spans: List[Span] = []
        for r in results:
            label = r.get("entity_group", r.get("entity", ""))
            label = label.replace("B-", "").replace("I-", "")
            mapped = self._label_map.get(label, "secret")
            spans.append(
                Span(
                    label=mapped,
                    text=r.get("word", r.get("text", "")),
                    start=r.get("start", 0),
                    end=r.get("end", 0),
                    score=r.get("score", 0.0),
                )
            )
        return spans
