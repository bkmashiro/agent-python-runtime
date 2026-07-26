#!/usr/bin/env python3
import argparse
import hashlib
import json
import pathlib
import stat
from typing import Any

MAX_BYTES = 1_048_576
MAX_MODELS = 512
DENIED_KEYS = {
    "authorization",
    "api_key",
    "apikey",
    "password",
    "access_token",
    "client_secret",
    "private_key",
}


class CatalogError(ValueError):
    pass


def reject_duplicate_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise CatalogError("duplicate JSON key")
        result[key] = value
    return result


def reject_sensitive_keys(value: Any) -> None:
    if isinstance(value, dict):
        for key, child in value.items():
            normalized = key.lower().replace("-", "_")
            if normalized in DENIED_KEYS:
                raise CatalogError("sensitive field in catalog")
            reject_sensitive_keys(child)
    elif isinstance(value, list):
        for child in value:
            reject_sensitive_keys(child)


def canonical_catalog(raw: bytes) -> tuple[bytes, int]:
    try:
        document = json.loads(raw, object_pairs_hook=reject_duplicate_pairs)
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise CatalogError("invalid catalog JSON") from exc
    if not isinstance(document, dict) or set(document) - {"object", "data"} or document.get("object") not in (None, "list"):
        raise CatalogError("invalid catalog envelope")
    models = document.get("data")
    if not isinstance(models, list) or not (1 <= len(models) <= MAX_MODELS):
        raise CatalogError("invalid model list")
    reject_sensitive_keys(models)
    seen: set[str] = set()
    normalized: list[dict[str, Any]] = []
    for model in models:
        if not isinstance(model, dict):
            raise CatalogError("invalid model entry")
        model_id = model.get("id")
        if not isinstance(model_id, str) or not (1 <= len(model_id) <= 128) or any(ord(ch) < 0x20 for ch in model_id) or model_id in seen:
            raise CatalogError("invalid or duplicate model id")
        seen.add(model_id)
        normalized.append(model)
    if "gpt-5.4" not in seen:
        raise CatalogError("target model is absent")
    normalized.sort(key=lambda item: item["id"])
    canonical = json.dumps(
        {"schema_version": "linkapi-model-catalog/v1", "data": normalized},
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")
    return canonical, len(normalized)


def read_bounded_regular(path: pathlib.Path) -> bytes:
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_size <= 0 or info.st_size > MAX_BYTES:
        raise CatalogError("catalog must be a bounded regular file")
    data = path.read_bytes()
    if len(data) != info.st_size:
        raise CatalogError("catalog changed while reading")
    return data


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("catalog")
    args = parser.parse_args()
    try:
        canonical, count = canonical_catalog(read_bounded_regular(pathlib.Path(args.catalog)))
    except (CatalogError, OSError, ValueError) as exc:
        raise SystemExit(f"catalog rejected: {exc}")
    print(json.dumps({
        "canonicalization": "linkapi-model-catalog/v1",
        "catalog_digest": "sha256:" + hashlib.sha256(canonical).hexdigest(),
        "models": count,
        "target_model": "gpt-5.4",
    }, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
