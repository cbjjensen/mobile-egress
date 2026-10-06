import importlib.util
from pathlib import Path
import sys
import unittest

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("auto_notify", Path(__file__).with_name("ios-external-auto-notify.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

SELECTION = {"appId": "app", "groupId": "group", "buildId": "build", "version": "2.0.0", "buildNumber": "7"}


def group():
    return {"data": {"id": "group", "type": "betaGroups", "attributes": {"isInternalGroup": False}, "relationships": {"app": {"data": {"type": "apps", "id": "app"}}}}}


def build(enabled=False, state="READY_FOR_BETA_SUBMISSION"):
    return {"data": {"id": "build", "type": "builds", "attributes": {"version": "7", "expired": False, "expirationDate": "2099-01-01T00:00:00Z", "processingState": "VALID", "buildAudienceType": "APP_STORE_ELIGIBLE"}, "relationships": {
        "app": {"data": {"type": "apps", "id": "app"}},
        "preReleaseVersion": {"data": {"type": "preReleaseVersions", "id": "version"}},
        "buildBetaDetail": {"data": {"type": "buildBetaDetails", "id": "detail"}},
    }}, "included": [
        {"id": "version", "type": "preReleaseVersions", "attributes": {"version": "2.0.0", "platform": "IOS"}},
        {"id": "detail", "type": "buildBetaDetails", "attributes": {"autoNotifyEnabled": enabled, "externalBuildState": state}},
    ]}


class FakeApple:
    def __init__(self, *responses):
        self.responses = list(responses)
        self.calls = []

    def __call__(self, method, path, payload=None):
        self.calls.append((method, path, payload))
        response = self.responses.pop(0)
        if isinstance(response, Exception):
            raise response
        return response


class AutoNotifyTests(unittest.TestCase):
    def test_check_is_read_only_and_reports_missing_flag(self):
        api = FakeApple(group(), build())
        result = module.prepare_auto_notify(api, SELECTION)
        self.assertFalse(result["verified"])
        self.assertTrue(result["changeRequired"])
        self.assertEqual([c[0] for c in api.calls], ["GET", "GET"])

    def test_already_enabled_release_is_a_verified_noop(self):
        api = FakeApple(group(), build(True, "IN_BETA_TESTING"))
        result = module.prepare_auto_notify(api, SELECTION, apply=True)
        self.assertTrue(result["verified"])
        self.assertFalse(result["changed"])
        self.assertEqual(len(api.calls), 2)

    def test_apply_changes_only_exact_detail_flag_and_reads_it_back(self):
        api = FakeApple(group(), build(), {}, {"data": {"type": "buildBetaDetails", "id": "detail", "attributes": {"autoNotifyEnabled": True}}})
        result = module.prepare_auto_notify(api, SELECTION, apply=True)
        self.assertTrue(result["verified"])
        self.assertTrue(result["changed"])
        self.assertEqual(api.calls[2], ("PATCH", "buildBetaDetails/detail", {"data": {"type": "buildBetaDetails", "id": "detail", "attributes": {"autoNotifyEnabled": True}}}))
        self.assertEqual(api.calls[3][:2], ("GET", "buildBetaDetails/detail"))

    def test_does_not_require_or_change_group_build_assignment(self):
        api = FakeApple(group(), build(True))
        self.assertTrue(module.prepare_auto_notify(api, SELECTION)["verified"])
        self.assertTrue(all("relationships/builds" not in call[1] for call in api.calls))

    def test_refuses_late_activation_or_unknown_outcome_retry(self):
        api = FakeApple(group(), build(False, "IN_BETA_TESTING"))
        with self.assertRaises(module.SafeError):
            module.prepare_auto_notify(api, SELECTION, apply=True)
        self.assertEqual(len(api.calls), 2)
        api = FakeApple(group(), build(), module.SafeError("Apple mutation outcome unknown; read state before retrying."))
        with self.assertRaises(module.SafeError):
            module.prepare_auto_notify(api, SELECTION, apply=True)
        self.assertEqual(len(api.calls), 3)

    def test_refuses_failed_readback(self):
        api = FakeApple(group(), build(), {}, {"data": {"type": "buildBetaDetails", "id": "detail", "attributes": {"autoNotifyEnabled": False}}})
        with self.assertRaises(module.SafeError):
            module.prepare_auto_notify(api, SELECTION, apply=True)

    def test_rejects_wrong_scope_incompatible_expired_or_internal_builds_without_writes(self):
        for fault in ["group-app", "internal-group", "build-app", "build-id", "version", "build-number", "expired", "audience", "platform", "missing-flag", "missing-detail"]:
            with self.subTest(fault=fault):
                g, b = group(), build()
                if fault == "group-app": g["data"]["relationships"]["app"]["data"]["id"] = "other"
                if fault == "internal-group": g["data"]["attributes"]["isInternalGroup"] = True
                if fault == "build-app": b["data"]["relationships"]["app"]["data"]["id"] = "other"
                if fault == "build-id": b["data"]["id"] = "other"
                if fault == "version": b["included"][0]["attributes"]["version"] = "1.0.0"
                if fault == "build-number": b["data"]["attributes"]["version"] = "5"
                if fault == "expired": b["data"]["attributes"]["expirationDate"] = "2020-01-01T00:00:00Z"
                if fault == "audience": b["data"]["attributes"]["buildAudienceType"] = "INTERNAL_ONLY"
                if fault == "platform": b["included"][0]["attributes"]["platform"] = "MAC_OS"
                if fault == "missing-flag": del b["included"][1]["attributes"]["autoNotifyEnabled"]
                if fault == "missing-detail": b["included"].pop()
                api = FakeApple(g, b)
                with self.assertRaises(module.SafeError): module.prepare_auto_notify(api, SELECTION, apply=True)
                self.assertTrue(all(call[0] == "GET" for call in api.calls))

    def test_rejects_known_old_floor_even_if_explicitly_requested(self):
        with self.assertRaises(module.SafeError):
            module.prepare_auto_notify(FakeApple(), {**SELECTION, "buildNumber": "5"}, apply=True)


if __name__ == "__main__":
    unittest.main()
