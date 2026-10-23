import json
import importlib.util
import tempfile
import unittest
from pathlib import Path


SPEC = importlib.util.spec_from_file_location(
    "summarize_e2e_baseline", Path(__file__).with_name("summarize-e2e-baseline.py")
)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("could not load summarize-e2e-baseline.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class SummarizeE2EBaselineTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.artifact_dir = Path(self.temp.name)
        (self.artifact_dir / "cases").mkdir()
        self.environment = {
            "schemaVersion": 2,
            "evidenceClass": "live-reproduction",
            "capturedAt": "2030-01-02T03:04:05Z",
            "distribution": "kind",
            "clusterName": "must-not-survive",
            "tuna": {
                "sourceCommit": "a" * 40,
                "sourceDirty": False,
                "goVersion": "go1.26.10",
            },
            "execution": {
                "expectedKubernetes": "1.37.0",
                "kindNodeImage": "kindest/node:v1.37.0@sha256:" + "b" * 64,
                "selectedCases": ["example"],
                "fullSuite": True,
            },
            "apiServer": {"gitVersion": "v1.37.0", "gitCommit": "c" * 40},
            "nodes": [
                {
                    "name": "must-not-survive",
                    "kubeletVersion": "v1.37.0",
                    "containerRuntimeVersion": "containerd://2.2.0",
                    "operatingSystem": "linux",
                    "osImage": "Test Linux",
                    "architecture": "arm64",
                    "kernelVersion": "6.0.0",
                }
            ],
        }
        self.case = {
            "focus": {"kind": "Service", "namespace": "secret", "name": "secret"},
            "cluster": {"kubernetesVersion": "v1.37.0"},
            "health": "degraded",
            "partial": False,
            "warnings": [],
            "rootCauses": ["finding-1"],
            "findings": [
                {
                    "id": "finding-1",
                    "type": "example-root",
                    "rootCause": True,
                    "resource": {"name": "must-not-survive"},
                },
                {
                    "id": "finding-2",
                    "type": "example-symptom",
                    "rootCause": False,
                },
            ],
            "rules": {"evaluated": 16},
        }

    def tearDown(self):
        self.temp.cleanup()

    def write_inputs(self):
        (self.artifact_dir / "environment.json").write_text(
            json.dumps(self.environment), encoding="utf-8"
        )
        (self.artifact_dir / "cases" / "example.json").write_text(
            json.dumps(self.case), encoding="utf-8"
        )

    def test_summarizes_outcomes_and_removes_resource_identities(self):
        self.write_inputs()
        baseline = MODULE.summarize(self.artifact_dir)

        self.assertEqual(["example-root"], baseline["cases"][0]["rootCauseTypes"])
        self.assertEqual(
            ["example-root", "example-symptom"],
            baseline["cases"][0]["findingTypes"],
        )
        encoded = json.dumps(baseline)
        self.assertNotIn("must-not-survive", encoded)
        self.assertNotIn('"namespace"', encoded)

    def test_rejects_dirty_source(self):
        self.environment["tuna"]["sourceDirty"] = True
        self.write_inputs()

        with self.assertRaisesRegex(ValueError, "clean Tuna source tree"):
            MODULE.summarize(self.artifact_dir)

    def test_rejects_root_flag_disagreement(self):
        self.case["rootCauses"] = []
        self.write_inputs()

        with self.assertRaisesRegex(ValueError, "rootCauses disagree"):
            MODULE.summarize(self.artifact_dir)

    def test_rejects_targeted_run_as_durable_baseline(self):
        self.environment["execution"]["fullSuite"] = False
        self.write_inputs()

        with self.assertRaisesRegex(ValueError, "successful full E2E suite"):
            MODULE.summarize(self.artifact_dir)


if __name__ == "__main__":
    unittest.main()
