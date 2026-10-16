"""Offline infrastructure safety checks; no AWS credentials or network used."""
import json
from pathlib import Path
import unittest
from urllib.parse import parse_qs, urlparse
from generate import application, foundation
from prepare_runtime import postgres_url


class SafetyTests(unittest.TestCase):
    def test_database_is_private_encrypted_recoverable(self):
        db = foundation()["Resources"]["Database"]
        self.assertFalse(db["Properties"]["PubliclyAccessible"])
        self.assertTrue(db["Properties"]["StorageEncrypted"])
        self.assertTrue(db["Properties"]["MultiAZ"])
        self.assertTrue(db["Properties"]["DeletionProtection"])
        self.assertEqual(db["DeletionPolicy"], "Snapshot")

    def test_no_accidental_tasks_or_dns_cutover(self):
        template = application()
        self.assertEqual(template["Parameters"]["EnableService"]["Default"], "false")
        self.assertEqual(template["Resources"]["ScaleTarget"]["Condition"], "Enabled")
        self.assertFalse(any(r["Type"] == "AWS::Route53::RecordSet" for r in template["Resources"].values()))
        self.assertEqual(template["Parameters"]["MaxApiTasks"]["MaxValue"], 10)
        for name in ("ApiService", "WorkerService"):
            props = template["Resources"][name]["Properties"]
            self.assertEqual(props["DesiredCount"]["Fn::If"][-1], 0)
            self.assertEqual(props["NetworkConfiguration"]["AwsvpcConfiguration"]["AssignPublicIp"], "DISABLED")

    def test_no_inline_workers_or_migrations_in_replicas(self):
        tasks = application()["Resources"]
        for name in ("ApiTask", "WorkerTask"):
            container = tasks[name]["Properties"]["ContainerDefinitions"][0]
            env = {e["Name"]: e["Value"] for e in container["Environment"]}
            self.assertEqual(env["RUN_MIGRATIONS"], "false")
            self.assertEqual(env["RUN_INLINE_WORKERS"], "false")
            self.assertEqual(env["REDIS_OPTIONAL"], "false")
            self.assertGreaterEqual(container["StopTimeout"], 75)

    def test_database_url_handles_password_characters_and_validates_tls(self):
        url = urlparse(postgres_url("example.rds.amazonaws.com", "user", "a:b/@?+%"))
        self.assertEqual(url.hostname, "example.rds.amazonaws.com")
        self.assertEqual(parse_qs(url.query)["sslmode"], ["verify-full"])
        self.assertNotIn("a:b/@?+%", url.geturl())

    def test_generated_templates_are_current(self):
        for name, build in (("foundation", foundation), ("application", application)):
            self.assertEqual(json.loads((Path(__file__).parent / f"{name}.json").read_text()), build())


if __name__ == "__main__": unittest.main()
