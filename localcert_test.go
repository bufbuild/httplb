// Copyright 2023-2025 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package httplb

// Certificate for localhost. This is similar to the certificate built-into Go,
// but it's signed for localhost instead of 127.0.0.1.

const (
	localhostCert = `-----BEGIN CERTIFICATE-----
MIIC+TCCAeGgAwIBAgIQGLhijh7o3ERutjzb38I8ATANBgkqhkiG9w0BAQsFADAS
MRAwDgYDVQQKEwdBY21lIENvMB4XDTIzMDYzMDE2MjA0OFoXDTQzMDYyNTE2MjA0
OFowEjEQMA4GA1UEChMHQWNtZSBDbzCCASIwDQYJKoZIhvcNAQEBBQADggEPADCC
AQoCggEBALEJjxqphaR5/+ks0Q8hZsfRE/VkWNxLYYnJ2bApdqqPwoxc9j0HyRuK
Em0hGMZEj+VS1hiXzFL7zXOZEKbnNv9Pd60z1nOWXBh4AYk1pPXF86K4FMmm4qPg
5atsem41aDsCYQrBi70RCcsUWt6WhLkZWt9tPc2hCfjfhavJ5nXMxI7AppsIPMQc
qJpGg8yYg60Uu8CLRoX5vZ87ehl7BVdOSK0bigUDcCZkvTgzzJU8UyI1P8xNu3rk
beRjFHBaKWVeEPp8nHDVDNKWOKVqa5fWyaatHudPxF6JkQeGSJw+Y9ZgpKDrjZBL
AaT7evjEC69S+yPLHEb8iaINZjV33KkCAwEAAaNLMEkwDgYDVR0PAQH/BAQDAgWg
MBMGA1UdJQQMMAoGCCsGAQUFBwMBMAwGA1UdEwEB/wQCMAAwFAYDVR0RBA0wC4IJ
bG9jYWxob3N0MA0GCSqGSIb3DQEBCwUAA4IBAQBF69a9kLZ2zF2UraLeuLe36QRX
sHh7d9LgcXm4pqB7IBqJt/wf5obKJokO6EAGEVnLHhSndl4o/dKV5V2F55G9gxVl
e9HUpY6nhvjFoAUl3YXYW+fPbcoEhJnQs+R0j3bDKDn/bTQtj7qrV+AWEB2B0I3L
MO0Ix6zmuB4nhMUQxDIR5uVqc8J/1BtuvVm+UtC40e8dqVJ6PZm6w/o33+WdNor/
Mvs4FRn9YKneh+Qis+bVJEYnlh5kxuWeS3949X+Knpp66xirT7Gl+LTT4bd2Z+K8
+RYZt+PuGXn8sMwWyUoneuZF0n3i9Hap9D6U46XQ5zSma2pbfq3F6Oe67NMc
-----END CERTIFICATE-----`
	localhostKey = `-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQCxCY8aqYWkef/p
LNEPIWbH0RP1ZFjcS2GJydmwKXaqj8KMXPY9B8kbihJtIRjGRI/lUtYYl8xS+81z
mRCm5zb/T3etM9ZzllwYeAGJNaT1xfOiuBTJpuKj4OWrbHpuNWg7AmEKwYu9EQnL
FFreloS5GVrfbT3NoQn434WryeZ1zMSOwKabCDzEHKiaRoPMmIOtFLvAi0aF+b2f
O3oZewVXTkitG4oFA3AmZL04M8yVPFMiNT/MTbt65G3kYxRwWillXhD6fJxw1QzS
ljilamuX1smmrR7nT8ReiZEHhkicPmPWYKSg642QSwGk+3r4xAuvUvsjyxxG/Imi
DWY1d9ypAgMBAAECggEBAJwSgUZQDLFjnGhESkm8eI/PknjTbkNHcUW33WGgLC1R
b4GusqY7JuBQaM4sT1r7NqBE1tn3ePnvYsB2QGfjjmil9iuLd5OPCsHHihMcZ8EE
MjVRc4ISzdsLeW4WxBhEnQ7omgSRfE/BpZCS8UkqCPflkmdGNyYAwnnVFFLOO428
8EA5OE1z8kA78+gljONPaK8NQR9drt0iEllvqXBaOMi/iM4e9hE9cx93HW3yxfC4
aZrMIEyjNqf6qx35Nr34yjUXJKyoNqNTuopEij+/frzXT3kCfVtQFq3s5VFMIsVi
WLkIQESppLpdXF82sPkLN8y4/xsOaOWzrE805OgW6AECgYEA26P0i4Uorbtt5x3P
+eDoG76x37WucTihzsOQve0jAj6pAZ1kPT/umE8NSwQNjsQcXKkiir4bObuvxTKa
fjAFp+QsBawXnZMuslYDHs/X/ZaE6wg0PfYtBunA8hw36JNAIW+GgJ6F/ZYF/UgJ
1ZWnIKVg+c+F7Gtf6AGk8g9dJNkCgYEAzlglQNe3JnIufUR8QkcozXV8GOKHacc0
+/5TTzVUyAcrML1xqbYtkOOz6AIGrRsTOheRWLZFxoktoG6yuwOBND0zoagW/z10
Bx+i0clVwDAnfbRw1uJ4zBG6bXFcdxGwx7QxlU/7dnAWRImqLv6Z+OYj3MzxlbcL
pKmMTt8oVFECgYEAsMT2xudHgvNrE2wJ+1jIVbQXIi3tlE/44hjBQCo/V8ooaRVM
HIN8unY9A5fidXlePjEdjL5N2Rw17aa5cj+h/aqEx5fmdbqEBaF153FtqzleBm7W
5NthB8RPtkuBr5v7LC2++Xsb6ai5b0xwJcbI+FxBfSxI46rTSD0yjGJTG5kCgYBP
5MMv0xYf9a/YYs713pWGz8ln3TXvF+mE9FkPXyffdx8a9Q7wVhBYfEGpQDeTiNst
7/gf8BseHvkimBnt3RKGxneaTPnyg7nMFEy3i4v/KOXxfw79tJxu7yJOw8i4dYoM
GNHl7R0BI68LhH33Si8Vtw4FrPiRLll8vQUNeMwlsQKBgFj07OqyYezgrYe50ZZN
eOOV090RXHFDdYmMMQBmCc9sNDtxiQ8Zc6gJZP09gG26Z+PYybN5PFxjgbSlhdJL
UQWMvzk4x8bDvL+rMKF2B1W6kDQLGB/AwqGor2n/4LsCxdyvpSjHsZEGHREboVYW
NSyYDGt1191pg9bsaGV1XqZK
-----END PRIVATE KEY-----`
)
