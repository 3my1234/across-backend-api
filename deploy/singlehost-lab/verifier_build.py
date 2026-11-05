"""Build a Linux verifier compatible with the immutable Alpine test image."""
import struct

INTERPRETER=b'/lib/ld-musl-x86_64.so.1\0'

def build_args(destination):
    # purego dependencies can add PT_INTERP even with CGO_ENABLED=0. The
    # Windows cross-linker's default glibc loader is absent from Alpine.
    return ['go','build','-trimpath','-ldflags=-s -w -I /lib/ld-musl-x86_64.so.1','-o',str(destination),'./cmd/capacity-lab']

def validate_binary(path):
    data=path.read_bytes()
    if len(data)<64 or data[:6]!=b'\x7fELF\x02\x01' or struct.unpack_from('<H',data,18)[0]!=62:
        raise ValueError('Linux amd64 ELF verifier required')
    offset=struct.unpack_from('<Q',data,32)[0]
    size,count=struct.unpack_from('<HH',data,54)
    if size<56 or offset+size*count>len(data):raise ValueError('invalid ELF program headers')
    for index in range(count):
        entry=offset+index*size
        if struct.unpack_from('<I',data,entry)[0]==3:
            start=struct.unpack_from('<Q',data,entry+8)[0]
            length=struct.unpack_from('<Q',data,entry+32)[0]
            if data[start:start+length]!=INTERPRETER:raise ValueError('verifier loader is absent from Alpine image')
