#!/usr/bin/env python3
"""Apply the bounded NumPy unique.cpp no-exception probe adaptation."""

from __future__ import annotations

import argparse
from pathlib import Path


REPLACEMENTS = (
    (
        "allocation seam",
        """    using set_type = std::unordered_set<npy_static_string *,
                                        decltype(hash), decltype(equal)>;
    set_type hashset(std::min(isize, HASH_TABLE_INITIAL_BUCKETS), hash, equal);

    {
""",
        """    using set_type = std::unordered_set<npy_static_string *,
                                        decltype(hash), decltype(equal)>;
    set_type hashset(std::min(isize, HASH_TABLE_INITIAL_BUCKETS), hash, equal);

    bool load_failed = false;
    {
""",
    ),
    (
        "string-load throw seam",
        """            if (is_null == -1) {
                // Unexpected error. Throw a C++ exception that will be caught
                // by the caller of unique_vstring() and converted into a Python
                // RuntimeError.
                throw std::runtime_error("Failed to load string from packed "
                                         "static string.");
            }
""",
        """            if (is_null == -1) {
                load_failed = true;
                break;
            }
""",
    ),
    (
        "post-load error seam",
        """            hashset.insert(&unpacked_strings[i]);
        }
    }

    PyArrayObject *res_obj = empty_array_like(self, hashset.size());
""",
        """            hashset.insert(&unpacked_strings[i]);
        }
    }
    if (load_failed) {
        PyErr_SetString(PyExc_RuntimeError,
                        "Failed to load string from packed static string.");
        return NULL;
    }

    PyArrayObject *res_obj = empty_array_like(self, hashset.size());
""",
    ),
    (
        "exception translation seam",
        """    PyObject *result = NULL;
    try {
        auto type = PyArray_TYPE(arr);
        // we only support data types present in our unique_funcs map
        if (unique_funcs.find(type) == unique_funcs.end()) {
            result = Py_NewRef(Py_NotImplemented);
        }
        else {
            result = unique_funcs[type](arr, equal_nan);
        }
    }
    catch (const std::bad_alloc &e) {
        PyErr_NoMemory();
        result = NULL;
    }
    catch (const std::exception &e) {
        PyErr_SetString(PyExc_RuntimeError, e.what());
        result = NULL;
    }
""",
        """    // Probe-only no-EH path. Standard-library allocation failure uses
    // the WASI SDK noeh runtime semantics and is not production-qualified.
    PyObject *result = NULL;
    auto type = PyArray_TYPE(arr);
    // we only support data types present in our unique_funcs map
    if (unique_funcs.find(type) == unique_funcs.end()) {
        result = Py_NewRef(Py_NotImplemented);
    }
    else {
        result = unique_funcs[type](arr, equal_nan);
    }
""",
    ),
)

FORBIDDEN_EXCEPTION_SYNTAX = ("throw std::", "catch (const std::", "    try {")


def patch_text(text: str) -> str:
    for name, old, new in REPLACEMENTS:
        if text.count(old) != 1:
            raise ValueError(
                f"expected exactly one NumPy no-exception unique hash {name}"
            )
        text = text.replace(old, new)
    if any(token in text for token in FORBIDDEN_EXCEPTION_SYNTAX):
        raise ValueError("unexpected C++ exception syntax remains in NumPy unique.cpp")
    return text


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("source", type=Path)
    args = parser.parse_args()
    original = args.source.read_text()
    args.source.write_text(patch_text(original))


if __name__ == "__main__":
    main()
