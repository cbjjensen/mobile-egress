import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const policyPath = new URL('../src/aws-permissions-policy.json', import.meta.url)
const appPath = new URL('../src/App.tsx', import.meta.url)

test('advertised AWS policy authorizes SSM profile setup and confirmed reboot recovery', async () => {
  const policy = JSON.parse(await readFile(policyPath, 'utf8'))
  const actions = new Set(policy.Statement.flatMap(statement => statement.Action ?? []))
  const requiredActions = [
    'ec2:AssociateIamInstanceProfile',
    'ec2:RebootInstances',
    'iam:AddRoleToInstanceProfile',
    'iam:AttachRolePolicy',
    'iam:CreateInstanceProfile',
    'iam:CreateRole',
    'iam:GetInstanceProfile',
    'iam:GetRole',
    'iam:ListAttachedRolePolicies',
    'iam:ListRolePolicies',
    'iam:PassRole',
    'iam:TagInstanceProfile',
    'iam:TagRole',
  ]

  assert.deepEqual(
    requiredActions.filter(action => !actions.has(action)),
    [],
    'the copied policy is missing an action used by SSM setup or confirmed recovery',
  )
})

test('SSM setup preserves the backend failure instead of replacing it with a generic message', async () => {
  const source = await readFile(appPath, 'utf8')

  assert.doesNotMatch(source, /throw new Error\('Unable to create the dedicated SSM profile\.'\)/)
  assert.match(source, /catch \(reason\)/)
})
