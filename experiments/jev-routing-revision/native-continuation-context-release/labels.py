import re


def normalize_label(text):
    trimmed = text.strip()
    lowered = trimmed.lower()
    return re.sub(r"\s+", "-", lowered)
