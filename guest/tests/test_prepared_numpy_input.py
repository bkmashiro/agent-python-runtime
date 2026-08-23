import hashlib
import json
import struct
import unittest

import agent_runtime


def digest(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def descriptor(body: bytes, **changes) -> str:
    value = {
        "schema_version": "pysolate.prepared-numpy-input.v1",
        "name": "dataset",
        "codec": "numpy_ndarray_c_v1",
        "dtype": "<i8",
        "shape": [2, 2],
        "order": "C",
        "endianness": "little",
        "nbytes": len(body),
        "body_sha256": digest(body),
        "input_sha256": "sha256:" + "a" * 64,
    }
    value.update(changes)
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


class PreparedNumpyInputTests(unittest.TestCase):
    def setUp(self) -> None:
        agent_runtime._initialize("{}")
        self.body = struct.pack("<qqqq", 1, 2, 3, 4)

    def test_prepares_private_mutable_c_contiguous_array_once(self) -> None:
        agent_runtime._prepare_numpy_ndarray(descriptor(self.body), self.body)
        array = agent_runtime._prepared_globals["dataset"]
        self.assertEqual("<i8", array.dtype.str)
        self.assertEqual((2, 2), array.shape)
        self.assertTrue(array.flags.c_contiguous)
        array[0, 0] = 99
        self.assertEqual(99, int(array[0, 0]))
        with self.assertRaisesRegex(RuntimeError, "already prepared"):
            agent_runtime._prepare_numpy_ndarray(descriptor(self.body), self.body)

    def test_rejects_descriptor_body_and_name_drift(self) -> None:
        cases = (
            (descriptor(self.body, nbytes=len(self.body) - 1), self.body),
            (descriptor(self.body, body_sha256="sha256:" + "b" * 64), self.body),
            (descriptor(self.body, name="__builtins__"), self.body),
            (descriptor(self.body, shape=[4, 2]), self.body),
            (descriptor(self.body) + " ", self.body),
            (descriptor(self.body), self.body[:-1]),
        )
        for metadata, body in cases:
            with self.subTest(metadata=metadata, body_bytes=len(body)):
                agent_runtime._initialize("{}")
                with self.assertRaises((TypeError, ValueError)):
                    agent_runtime._prepare_numpy_ndarray(metadata, body)

    def test_initialize_clears_prepared_value(self) -> None:
        agent_runtime._prepare_numpy_ndarray(descriptor(self.body), self.body)
        agent_runtime._initialize("{}")
        self.assertNotIn("dataset", agent_runtime._prepared_globals)


if __name__ == "__main__":
    unittest.main()
