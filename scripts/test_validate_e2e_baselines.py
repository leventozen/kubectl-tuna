import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


SPEC = importlib.util.spec_from_file_location(
    "validate_e2e_baselines", Path(__file__).with_name("validate-e2e-baselines.py")
)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("could not load validate-e2e-baselines.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ValidateE2EBaselinesTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.commit = "a" * 40
        self.index = {
            "schemaVersion": 1,
            "sets": [
                {
                    "id": "run-123",
                    "repository": "owner/repository",
                    "workflowRun": 123,
                    "sourceCommit": self.commit,
                    "baselines": [],
                }
            ],
        }

    def tearDown(self):
        self.temp.cleanup()

    def baseline(self, version: str, extra_finding: bool = False) -> dict:
        findings = ["example-root"]
        if extra_finding:
            findings.append("version-specific-symptom")
        return {
            "schemaVersion": 1,
            "evidenceClass": "live-reproduction",
            "capturedAt": "2030-01-02T03:04:05Z",
            "tuna": {"sourceCommit": self.commit, "goVersion": "go1.26.10"},
            "suite": {"full": True, "selectedCases": ["example"]},
            "kubernetes": {
                "distribution": "kind",
                "expectedVersion": version,
                "kindNodeImage": f"kindest/node:v{version}@sha256:" + "b" * 64,
                "apiServer": {"gitVersion": f"v{version}"},
                "nodes": [{"kubeletVersion": f"v{version}"}],
            },
            "cases": [
                {
                    "id": "example",
                    "focusKind": "Service",
                    "health": "degraded",
                    "partial": False,
                    "findingTypes": findings,
                    "rootCauseTypes": ["example-root"],
                    "warningSources": [],
                    "rules": {
                        "evaluated": 1,
                        "skippedIDs": [],
                        "suspended": False,
                    },
                }
            ],
        }

    def add_baseline(self, version: str, *, extra_finding: bool = False) -> Path:
        relative = f"run-123/kubernetes-{version}.json"
        entry = {
            "artifact": f"tuna-e2e-kubernetes-{version}",
            "artifactDigest": "sha256:" + "c" * 64,
            "kubernetesVersion": version,
            "path": relative,
        }
        self.index["sets"][0]["baselines"].append(entry)
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(
            json.dumps(self.baseline(version, extra_finding)), encoding="utf-8"
        )
        return path

    def write_index(self) -> None:
        (self.root / "index.json").write_text(
            json.dumps(self.index), encoding="utf-8"
        )

    def test_accepts_consistent_set_and_allows_non_root_finding_drift(self):
        self.add_baseline("1.36.4")
        self.add_baseline("1.37.0", extra_finding=True)
        self.write_index()

        self.assertEqual({"sets": 1, "baselines": 2}, MODULE.validate(self.root))

    def test_rejects_cross_version_root_outcome_drift(self):
        self.add_baseline("1.36.4")
        second = self.add_baseline("1.37.0")
        value = json.loads(second.read_text(encoding="utf-8"))
        value["cases"][0]["rootCauseTypes"] = []
        second.write_text(json.dumps(value), encoding="utf-8")
        self.write_index()

        with self.assertRaisesRegex(ValueError, "root outcomes differ"):
            MODULE.validate(self.root)

    def test_rejects_resource_identity_keys(self):
        path = self.add_baseline("1.37.0")
        value = json.loads(path.read_text(encoding="utf-8"))
        value["cases"][0]["namespace"] = "private"
        path.write_text(json.dumps(value), encoding="utf-8")
        self.write_index()

        with self.assertRaisesRegex(ValueError, "forbidden identity key"):
            MODULE.validate(self.root)

    def test_rejects_unindexed_json(self):
        self.add_baseline("1.37.0")
        self.write_index()
        (self.root / "stray.json").write_text("{}", encoding="utf-8")

        with self.assertRaisesRegex(ValueError, "unindexed=.*stray.json"):
            MODULE.validate(self.root)


class CommittedE2EBaselinesTest(unittest.TestCase):
    def test_committed_baselines_validate(self):
        root = Path(__file__).parents[1] / "testdata" / "compatibility" / "baselines"
        result = MODULE.validate(root)
        self.assertGreaterEqual(result["sets"], 1)
        self.assertGreaterEqual(result["baselines"], 4)


if __name__ == "__main__":
    unittest.main()
