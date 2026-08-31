import assert from 'node:assert/strict'
import test from 'node:test'

let managedNodeIdentity
try {
  ({ managedNodeIdentity } = await import('../src/managed-node.js'))
} catch {
  managedNodeIdentity = undefined
}

const inventory = [{
  id: 'i-025e892bf1cb846f1',
  name: 'Bot 3.0',
  state: 'running',
  platform: 'windows',
  architecture: 'x86_64',
  imageDescription: 'Microsoft Windows Server 2019 with Desktop Experience Locale English AMI provided by Amazon',
  profileArn: 'arn:aws:iam::123456789012:instance-profile/MobileEgressSSM-025e892bf1cb846f1',
  roleName: 'MobileEgressSSM-025e892bf1cb846f1',
  ssmOnline: true,
}]

test('managed node identity uses its EC2 inventory name', () => {
  assert.deepEqual(managedNodeIdentity?.('i-025e892bf1cb846f1', inventory), {
    title: 'Bot 3.0',
    instanceId: 'i-025e892bf1cb846f1',
  })
})

test('managed node identity falls back when inventory is unavailable', () => {
  assert.deepEqual(managedNodeIdentity?.('i-0ab784ba09fc487ae', inventory), {
    title: 'i-0ab784ba09fc487ae',
    instanceId: '',
  })
})
