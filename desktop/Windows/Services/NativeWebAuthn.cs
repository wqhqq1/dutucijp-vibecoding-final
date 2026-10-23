using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json.Nodes;

namespace Reconciliation.Windows.Services;
// ABI layout follows the installed Windows SDK webauthn.h.
// Cancellation requires MakeCredential options v2 and GetAssertion options v3.
public static class NativeWebAuthn
{
    internal const uint MakeOptionsVersion = 2, GetOptionsVersion = 3;
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct RP
    {
        public uint Version;
        [MarshalAs(UnmanagedType.LPWStr)]
        public string ID;
        [MarshalAs(UnmanagedType.LPWStr)]
        public string Name;
        public IntPtr Icon;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct User
    {
        public uint Version, Size;
        public IntPtr ID;
        [MarshalAs(UnmanagedType.LPWStr)]
        public string Name;
        public IntPtr Icon;
        [MarshalAs(UnmanagedType.LPWStr)]
        public string Display;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct Client
    {
        public uint Version, Size;
        public IntPtr Data;
        [MarshalAs(UnmanagedType.LPWStr)]
        public string Hash;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct Cose
    {
        public uint Version;
        [MarshalAs(UnmanagedType.LPWStr)]
        public string Type;
        public int Alg;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct List
    {
        public uint Count;
        public IntPtr Values;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct Credential
    {
        public uint Version, Size;
        public IntPtr ID;
        [MarshalAs(UnmanagedType.LPWStr)]
        public string Type;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct MakeOptions
    {
        public uint Version, Timeout;
        public List Exclude, Extensions;
        public uint Attachment;
        public int Resident;
        public uint Verification, Attestation, Flags;
        public IntPtr Cancellation;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct GetOptions
    {
        public uint Version, Timeout;
        public List Allow, Extensions;
        public uint Attachment, Verification, Flags;
        public IntPtr AppID, UsedAppID, Cancellation;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct Attestation
    {
        public uint Version;
        public IntPtr Format;
        public uint AuthSize;
        public IntPtr AuthData;
        public uint AttestationSize;
        public IntPtr AttestationData;
        public uint DecodeType;
        public IntPtr Decode;
        public uint ObjectSize;
        public IntPtr Object;
        public uint CredentialSize;
        public IntPtr Credential;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct Assertion
    {
        public uint Version, AuthSize;
        public IntPtr AuthData;
        public uint SignatureSize;
        public IntPtr Signature;
        public Credential Credential;
        public uint UserSize;
        public IntPtr User;
    }

    [DllImport("webauthn.dll", ExactSpelling = true)]
    static extern int WebAuthNAuthenticatorMakeCredential(IntPtr hwnd, ref RP rp, ref User user, ref List parameters, ref Client client, ref MakeOptions options, out IntPtr result);
    [DllImport("webauthn.dll", CharSet = CharSet.Unicode, ExactSpelling = true)]
    static extern int WebAuthNAuthenticatorGetAssertion(IntPtr hwnd, string rp, ref Client client, ref GetOptions options, out IntPtr result);
    [DllImport("webauthn.dll", ExactSpelling = true)]
    static extern int WebAuthNGetCancellationId(out Guid id);
    [DllImport("webauthn.dll", ExactSpelling = true)]
    static extern int WebAuthNCancelCurrentOperation(ref Guid id);
    [DllImport("webauthn.dll", ExactSpelling = true)]
    static extern void WebAuthNFreeCredentialAttestation(IntPtr p);
    [DllImport("webauthn.dll", ExactSpelling = true)]
    static extern void WebAuthNFreeAssertion(IntPtr p);
    [DllImport("webauthn.dll", ExactSpelling = true)]
    static extern IntPtr WebAuthNGetErrorName(int hr);
    public static byte[] Decode(string s) => Convert.FromBase64String(s.Replace('-', '+').Replace('_', '/') + new string ('=', (4 - s.Length % 4) % 4));
    public static string Encode(byte[] b) => Convert.ToBase64String(b).TrimEnd('=').Replace('+', '-').Replace('/', '_');
    static byte[] Read(IntPtr p, uint size)
    {
        var bytes = new byte[size];
        if (size > 0)
            Marshal.Copy(p, bytes, 0, checked((int)size));
        return bytes;
    }

    sealed class Arena : IDisposable
    {
        readonly List<IntPtr> pointers = [];
        public IntPtr Bytes(byte[] b)
        {
            var p = Marshal.AllocHGlobal(Math.Max(b.Length, 1));
            pointers.Add(p);
            if (b.Length > 0)
                Marshal.Copy(b, 0, p, b.Length);
            return p;
        }

        public IntPtr Structs<T>(T[] values)
            where T : struct
        {
            var size = Marshal.SizeOf<T>();
            var p = Marshal.AllocHGlobal(Math.Max(1, size * values.Length));
            pointers.Add(p);
            for (int i = 0; i < values.Length; i++)
                Marshal.StructureToPtr(values[i], p + i * size, false);
            cleanups.Add(() =>
            {
                for (int i = 0; i < values.Length; i++)
                    Marshal.DestroyStructure<T>(p + i * size);
            });
            return p;
        }

        readonly List<Action> cleanups = [];
        public void Dispose()
        {
            foreach (var c in cleanups)
                c();
            foreach (var p in pointers)
                Marshal.FreeHGlobal(p);
        }
    }

    public static async Task<JsonNode> Perform(JsonNode options, NativeBinding binding, bool register, CancellationToken ct)
    {
        var hwnd = WinRT.Interop.WindowNative.GetWindowHandle(App.Window);
        // Capture UI-owned data before moving the blocking Windows operation to a worker.
        return await Task.Run(() =>
        {
            ct.ThrowIfCancellationRequested();
            using var arena = new Arena();
            var id = Guid.Empty;
            Marshal.ThrowExceptionForHR(WebAuthNGetCancellationId(out id));
            var cancelPointer = arena.Structs([id]);
            using var cancel = ct.Register(() => WebAuthNCancelCurrentOperation(ref id));
            var clientBytes = Encoding.UTF8.GetBytes(new JsonObject { ["type"] = register ? "webauthn.create" : "webauthn.get", ["challenge"] = options.S("challenge"), ["origin"] = binding.Origin, ["crossOrigin"] = false }.ToJsonString());
            var client = new Client
            {
                Version = 1,
                Size = (uint)clientBytes.Length,
                Data = arena.Bytes(clientBytes),
                Hash = "SHA-256"
            };
            Credential[] credentials = options.A(register ? "excludeCredentials" : "allowCredentials").Select(n =>
            {
                var bytes = Decode(n.S("id"));
                return new Credential
                {
                    Version = 1,
                    Size = (uint)bytes.Length,
                    ID = arena.Bytes(bytes),
                    Type = "public-key"
                };
            }).ToArray();
            var list = new List
            {
                Count = (uint)credentials.Length,
                Values = arena.Structs(credentials)
            };
            int hr;
            IntPtr result = IntPtr.Zero;
            try
            {
                if (register)
                {
                    var rp = new RP
                    {
                        Version = 1,
                        ID = binding.RpId,
                        Name = options["rp"].S("name")
                    };
                    var userNode = options["user"]!;
                    var userBytes = Decode(userNode.S("id"));
                    var user = new User
                    {
                        Version = 1,
                        Size = (uint)userBytes.Length,
                        ID = arena.Bytes(userBytes),
                        Name = userNode.S("name"),
                        Display = userNode.S("displayName")
                    };
                    var parameters = options.A("pubKeyCredParams").Select(n => new Cose { Version = 1, Type = "public-key", Alg = n.I("alg") }).ToArray();
                    var cose = new List
                    {
                        Count = (uint)parameters.Length,
                        Values = arena.Structs(parameters)
                    };
                    var make = new MakeOptions
                    {
                        Version = MakeOptionsVersion,
                        Timeout = 120000,
                        Exclude = list,
                        Resident = 1,
                        Verification = 1,
                        Attestation = 1,
                        Cancellation = cancelPointer
                    };
                    hr = WebAuthNAuthenticatorMakeCredential(hwnd, ref rp, ref user, ref cose, ref client, ref make, out result);
                    if (hr < 0)
                        throw new InvalidOperationException("Passkey 未完成：" + Marshal.PtrToStringUni(WebAuthNGetErrorName(hr)));
                    var a = Marshal.PtrToStructure<Attestation>(result);
                    var raw = Read(a.Credential, a.CredentialSize);
                    return new JsonObject
                    {
                        ["id"] = Encode(raw),
                        ["rawId"] = Encode(raw),
                        ["type"] = "public-key",
                        ["clientExtensionResults"] = new JsonObject(),
                        ["response"] = new JsonObject
                        {
                            ["clientDataJSON"] = Encode(clientBytes),
                            ["attestationObject"] = Encode(Read(a.Object, a.ObjectSize)),
                            ["transports"] = new JsonArray()
                        }
                    };
                }
                else
                {
                    var get = new GetOptions
                    {
                        Version = GetOptionsVersion,
                        Timeout = 120000,
                        Allow = list,
                        Verification = 1,
                        Cancellation = cancelPointer
                    };
                    hr = WebAuthNAuthenticatorGetAssertion(hwnd, binding.RpId, ref client, ref get, out result);
                    if (hr < 0)
                        throw new InvalidOperationException("Passkey 未完成：" + Marshal.PtrToStringUni(WebAuthNGetErrorName(hr)));
                    var a = Marshal.PtrToStructure<Assertion>(result);
                    var raw = Read(a.Credential.ID, a.Credential.Size);
                    return new JsonObject
                    {
                        ["id"] = Encode(raw),
                        ["rawId"] = Encode(raw),
                        ["type"] = "public-key",
                        ["clientExtensionResults"] = new JsonObject(),
                        ["response"] = new JsonObject
                        {
                            ["clientDataJSON"] = Encode(clientBytes),
                            ["authenticatorData"] = Encode(Read(a.AuthData, a.AuthSize)),
                            ["signature"] = Encode(Read(a.Signature, a.SignatureSize)),
                            ["userHandle"] = a.UserSize == 0 ? null : Encode(Read(a.User, a.UserSize))
                        }
                    };
                }
            }
            finally
            {
                if (result != IntPtr.Zero)
                {
                    if (register)
                        WebAuthNFreeCredentialAttestation(result);
                    else
                        WebAuthNFreeAssertion(result);
                }
            }
        }, ct);
    }
}
