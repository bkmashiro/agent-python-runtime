import importlib.util
import io
import pathlib
import tarfile
import tempfile
import unittest


MODULE_PATH = pathlib.Path(__file__).parents[1] / "build" / "validate_cache_layer.py"
SPEC = importlib.util.spec_from_file_location("cache_layer_validator", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
validator = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validator)


class CacheLayerValidatorTests(unittest.TestCase):
    def write_archive(self, path: pathlib.Path, members: list[tuple[str, bytes]]) -> None:
        with tarfile.open(path, "w") as archive:
            for name, body in members:
                info = tarfile.TarInfo(name)
                info.size = len(body)
                archive.addfile(info, io.BytesIO(body))

    def test_accepts_complete_bounded_layer(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            archive = pathlib.Path(directory) / "layer.tar"
            self.write_archive(archive, [("downloads/a", b"a"), ("tools/a", b"b"), ("cpython/a", b"c")])
            validator.validate(archive)

    def test_rejects_traversal_and_incomplete_layers(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            archive = pathlib.Path(directory) / "layer.tar"
            self.write_archive(archive, [("../escape", b"x"), ("tools/a", b"b"), ("cpython/a", b"c")])
            with self.assertRaises(ValueError):
                validator.validate(archive)
            self.write_archive(archive, [("tools/a", b"b"), ("cpython/a", b"c")])
            with self.assertRaises(ValueError):
                validator.validate(archive)

    def test_rejects_escaping_symlink(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            archive = pathlib.Path(directory) / "layer.tar"
            with tarfile.open(archive, "w") as handle:
                for root in ("downloads", "tools", "cpython"):
                    info = tarfile.TarInfo(root + "/a")
                    info.size = 1
                    handle.addfile(info, io.BytesIO(b"x"))
                link = tarfile.TarInfo("tools/escape")
                link.type = tarfile.SYMTYPE
                link.linkname = "../../outside"
                handle.addfile(link)
            with self.assertRaises(ValueError):
                validator.validate(archive)


if __name__ == "__main__":
    unittest.main()
