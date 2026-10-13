def normalize_label(text):
    if not isinstance(text, str):
        raise ValueError("label must be a string, got %r" % (type(text).__name__,))
    return "-".join(text.split()).lower()
