import re


def normalize_tag(value):
    return re.sub(r'\s+', '-', value.strip().lower())
