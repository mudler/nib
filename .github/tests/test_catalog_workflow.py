"""Offline contract checks; action state-machine review is in catalog-action-review.md.
These are static integration checks, not a simulation of GitHub or remote writes.
"""
import pathlib
import unittest

WORKFLOW = pathlib.Path(__file__).parents[1] / 'workflows/update-catalog.yml'
class CatalogWorkflow(unittest.TestCase):
    def setUp(self):
        self.text = WORKFLOW.read_text()
    def test_initial_existing_and_closed_pr_use_reviewed_updater(self):
        # Pinned implementation creates an absent PR, updates an existing open PR,
        # and creates a fresh PR for a closed/merged predecessor if changes remain.
        self.assertIn('peter-evans/create-pull-request@22a9089034f40e5a961c8808d113e2c98fb63676', self.text)
        self.assertNotIn('gh pr create', self.text)
    def test_repeat_and_noop(self):
        self.assertIn('delete-branch: true', self.text)
        self.assertNotIn('git commit', self.text)
        self.assertNotIn('if: steps.diff', self.text) # updater must also handle stale branches
        self.assertIn('add-paths: |\n            llmprovider/catalog/catalog.json.zst\n            llmprovider/catalog/provenance.json', self.text)
    def test_base_advancement_and_owned_branch(self):
        self.assertIn('ref: master', self.text)
        self.assertIn('base: master', self.text)
        self.assertIn('branch: automation/oh-my-pi-catalog', self.text)
        self.assertNotIn('branch-suffix:', self.text)
    def test_serialized_refresh_and_review_output(self):
        self.assertIn('group: update-model-catalog', self.text)
        self.assertIn('cancel-in-progress: false', self.text)
        self.assertIn('go run gen_catalog.go -refresh', self.text)
        self.assertIn('catalog-delta.txt', self.text)
        self.assertIn('body-path:', self.text)
        self.assertIn('set -euo pipefail', self.text)
if __name__ == '__main__':
    unittest.main()
