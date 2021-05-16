// +build opencl

package hashers

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"github.com/ngirot/BruteForce/bruteforce/maths"
	"gitlab.com/ngirot/blackcl"
	"strings"
)

type hasherGpuSha256 struct {
	device           *blackcl.Device
	kernelDictionary *blackcl.Kernel
	kernelAlphabet   *blackcl.Kernel
	endianness       binary.ByteOrder
}

func NewHasherGpuSha256() Hasher {
	gpus, err := blackcl.GetDevices(blackcl.DeviceTypeGPU)
	if err == nil {
		for _, device := range gpus {
			device.AddProgram(kernelSourceImport)
			kernelTest := device.Kernel("sha256_crypt_kernel")

			var bigEndianResult = hashWithGpu(device, kernelTest, binary.BigEndian, []string{"test"})[0]

			var endianness binary.ByteOrder = binary.LittleEndian
			if hex.EncodeToString(bigEndianResult) == "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" {
				endianness = binary.BigEndian
			}

			return &hasherGpuSha256{device, device.Kernel("sha256_crypt_kernel"), device.Kernel("sha256_crypt_and_worder"), endianness}
		}
	}

	return nil
}

func (h *hasherGpuSha256) Example() string {
	return hex.EncodeToString(h.Hash([]string{"1234567890"})[0])
}

func (h *hasherGpuSha256) DecodeInput(data string) []byte {
	var result, _ = hex.DecodeString(data)
	return result
}

func (h *hasherGpuSha256) Hash(datas []string) [][]byte {
	return hashWithGpu(h.device, h.kernelDictionary, h.endianness, datas)
}

func (h *hasherGpuSha256) IsValid(data string) bool {
	return genericBase64Validator(h, data)
}

func (h *hasherGpuSha256) Compare(transformedData []byte, referenceData []byte) bool {
	return bytes.Equal(transformedData, referenceData)
}

func convert(s string) []byte {
	return []byte(s)
}

func buildByteBuffer(d *blackcl.Device, data []byte) (*blackcl.Bytes, error) {
	v, err := d.NewBytes(len(data))
	if err != nil {
		panic("could not allocate buffer")
	}

	err = <-v.Copy(data)
	if err != nil {
		panic("could not copy data to buffer")
	}
	return v, err
}

func buildUintBuffer(d *blackcl.Device, data []uint32) (*blackcl.Uint32, error) {
	v, err := d.NewUint32(len(data))
	if err != nil {
		panic("could not allocate buffer")
	}

	err = <-v.Copy(data)
	if err != nil {
		panic("could not copy data to buffer")
	}
	return v, err
}

func (h *hasherGpuSha256) ProcessWithWildcard(charSet []string, saltBefore string, saltAfter string, numberOfWildCards int, expectedDigest string) string {
	return processWithGpu(h.device, h.kernelAlphabet, h.endianness,
		charSet, saltBefore, saltAfter, numberOfWildCards, expectedDigest)
}

func processWithGpu(device *blackcl.Device, kernel *blackcl.Kernel, endianness binary.ByteOrder,
	charSet []string, saltBefore string, saltAfter string, numberOfWildCards int, expectedDigest string) string {

	var de, _ = hex.DecodeString(expectedDigest)
	digestExpected, _ := buildUintBuffer(device, []uint32{
		endianness.Uint32(de[0:4]),
		endianness.Uint32(de[4:8]),
		endianness.Uint32(de[8:12]),
		endianness.Uint32(de[12:16]),
		endianness.Uint32(de[16:20]),
		endianness.Uint32(de[20:24]),
		endianness.Uint32(de[24:28]),
		endianness.Uint32(de[28:32]),
	})
	defer digestExpected.Release()
	numberOfWordToTest := maths.PowInt(len(charSet), numberOfWildCards)
    wordSize := len(saltBefore)+len(saltAfter)+numberOfWildCards

	bBuffer, _ := buildByteBuffer(device, make([]byte, numberOfWordToTest*wordSize))
	defer bBuffer.Release()

	bSaltBefore, _ := buildByteBuffer(device, []byte(saltBefore+"X"))
	defer bSaltBefore.Release()

	bSaltBeforeSize, _ := buildUintBuffer(device, []uint32{uint32(len(saltBefore))})
	defer bSaltBeforeSize.Release()

	bSaltAfter, _ := buildByteBuffer(device, []byte(saltAfter+"X"))
	defer bSaltAfter.Release()

	bSaltAfterSize, _ := buildUintBuffer(device, []uint32{uint32(len(saltAfter))})
	defer bSaltAfterSize.Release()

	bNumberOfWildcards, _ := buildUintBuffer(device, []uint32{uint32(numberOfWildCards)})
	defer bNumberOfWildcards.Release()

	bMatchingWildcard, _ := buildByteBuffer(device, make([]byte, numberOfWildCards))
	defer bMatchingWildcard.Release()

	mergedCharset := []byte(strings.Join(charSet, ""))
	bCharSet, _ := buildByteBuffer(device, mergedCharset)
	defer bCharSet.Release()

	bCharSetSize, _ := buildUintBuffer(device, []uint32{uint32(len(mergedCharset))})
	defer bCharSetSize.Release()

	err := <-kernel.Global(numberOfWordToTest).Local(1).Run(
		bBuffer,
		bSaltBefore,
		bSaltBeforeSize,
		bSaltAfter,
		bSaltAfterSize,
		bNumberOfWildcards,
		bMatchingWildcard,
		bCharSet,
		bCharSetSize,
		digestExpected,
	)

	if err != nil {
		panic("could not run kernel")
	}

	result, err := bMatchingWildcard.Data()
	if err != nil {
		panic("could not get data from buffer")
	}

	for i := 0; i < numberOfWildCards; i++ {
		if result[i] != 0 {
			return string(result)
		}
	}

	return ""
}

func hashWithGpu(device *blackcl.Device, kernel *blackcl.Kernel, endianness binary.ByteOrder, datas []string) [][]byte {

	var sizeInput = make([]uint32, len(datas)+1)
	var encoded = make([][]byte, len(datas))
	var sumEncoded = 0
	sizeInput[0] = 0

	var previous = uint32(0)
	for i, s := range datas {
		/// Addresses
		var address = uint32(len(s)) + previous
		sizeInput[i+1] = address
		previous = address

		// Encoding
		var converted = convert(s)
		encoded[i] = converted
		sumEncoded += len(converted)
	}

	var textInput = make([]byte, sumEncoded)
	var position = 0
	for _, s := range encoded {
		var endPosition = position + len(s)
		copy(textInput[position:endPosition], s)
		position = endPosition
	}

	infos, _ := buildUintBuffer(device, sizeInput)
	defer infos.Release()

	key, _ := buildByteBuffer(device, textInput)
	defer key.Release()

	output, _ := buildUintBuffer(device, make([]uint32, 32*len(datas)))
	defer output.Release()

	err := <-kernel.Global(len(datas)).Local(1).Run(infos, key, output)
	if err != nil {
		panic("could not run kernel")
	}

	newData, err := output.Data()
	if err != nil {
		panic("could not get data from buffer")
	}

	var digests = make([][]byte, len(datas))
	for i := 0; i < len(datas); i++ {
		temp := make([]byte, 4*8)
		digests[i] = temp

		endianness.PutUint32(digests[i][0:4], newData[i*8])
		endianness.PutUint32(digests[i][4:8], newData[i*8+1])
		endianness.PutUint32(digests[i][8:12], newData[i*8+2])
		endianness.PutUint32(digests[i][12:16], newData[i*8+3])
		endianness.PutUint32(digests[i][16:20], newData[i*8+4])
		endianness.PutUint32(digests[i][20:24], newData[i*8+5])
		endianness.PutUint32(digests[i][24:28], newData[i*8+6])
		endianness.PutUint32(digests[i][28:32], newData[i*8+7])
	}

	return digests
}

// https://github.com/Fruneng/opencl_sha_al_im
const kernelSourceImport = `
#pragma OPENCL EXTENSION cl_khr_fp64 : enable

#ifndef uint32_t
#define uint32_t unsigned int
#endif

#define H0 0x6a09e667
#define H1 0xbb67ae85
#define H2 0x3c6ef372
#define H3 0xa54ff53a
#define H4 0x510e527f
#define H5 0x9b05688c
#define H6 0x1f83d9ab
#define H7 0x5be0cd19



uint rotr(uint x, int n) {
  if (n < 32) return (x >> n) | (x << (32 - n));
  return x;
}

uint ch(uint x, uint y, uint z) {
  return (x & y) ^ (~x & z);
}

uint maj(uint x, uint y, uint z) {
  return (x & y) ^ (x & z) ^ (y & z);
}

uint sigma0(uint x) {
  return rotr(x, 2) ^ rotr(x, 13) ^ rotr(x, 22);
}

uint sigma1(uint x) {
  return rotr(x, 6) ^ rotr(x, 11) ^ rotr(x, 25);
}

uint custom_pow(uint a, uint b) {
  int i;
  int result = 1;
  for(i=0;i<b;i++) {
    result *=a;
  }
  return result;
}

int comp_arrays(uint *a1, __global uint *a2, int size) {
  int i;
  for (i=0;i<size;i++) {
    if ( a1[i] != a2[i] ) return 0;
  }
  return 1;
}

uint gamma0(uint x) {
  return rotr(x, 7) ^ rotr(x, 18) ^ (x >> 3);
}

uint gamma1(uint x) {
  return rotr(x, 17) ^ rotr(x, 19) ^ (x >> 10);
}

void hash(global char *plain_key, uint *digest, uint ulen) {

//printf("%d => %s\n", index, plain_key);
//printf(">>> %d - %d - %d - %d - %d\n", data_info[0], data_info[1], data_info[2]);
//printf("=> %c\n", plain_key[0]);
//printf("3 %c\n", digest[0]);
//printf("dataInfos: %d - %d - %d\n", data_info[0], data_info[1], data_info[2]);
//printf("W %d => %d <-> %d\n", index, data_info[index], data_info[index+1]);
  int t, gid, msg_pad;
  int stop, mmod;
  uint i, item, total;
  uint W[80], temp, A,B,C,D,E,F,G,H,T1,T2;
  uint re = /*data_info[1]*/ 1;
  int current_pad;

  uint K[64]={
0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
};

  msg_pad=0;

  total = ulen%64>=56?2:1 + ulen/64;

  //printf("ulen: %u total:%u\n", ulen, total);

  digest[0] = H0;
  digest[1] = H1;
  digest[2] = H2;
  digest[3] = H3;
  digest[4] = H4;
  digest[5] = H5;
  digest[6] = H6;
  digest[7] = H7;
  for(item=0; item<total; item++)
  {

    A = digest[0];
    B = digest[1];
    C = digest[2];
    D = digest[3];
    E = digest[4];
    F = digest[5];
    G = digest[6];
    H = digest[7];

#pragma unroll
    for (t = 0; t < 80; t++){
    W[t] = 0x00000000;
    }
    msg_pad=item*64;
    if(ulen > msg_pad)
    {
      current_pad = (ulen-msg_pad)>64?64:(ulen-msg_pad);
    }
    else
    {
      current_pad =-1;    
    }

    //printf("current_pad: %d\n",current_pad);
    //printf("ulen: %d\n",ulen);
    //printf("msg_pad: %d\n",msg_pad);
    if(current_pad>0)
    {
      i=current_pad;

      stop =  i/4;
      //printf("i:%d, stop: %d msg_pad:%d\n",i,stop, msg_pad);
      for (t = 0 ; t < stop ; t++){
//printf("=> %s\n", plain_key[msg_pad + t * 4]);
        W[t] = ((uchar)  plain_key[msg_pad + t * 4]) << 24;
        W[t] |= ((uchar) plain_key[msg_pad + t * 4 + 1]) << 16;
        W[t] |= ((uchar) plain_key[msg_pad + t * 4 + 2]) << 8;
        W[t] |= (uchar)  plain_key[msg_pad + t * 4 + 3];
        //printf("W[%u]: %u\n",t,W[t]);
      }
      mmod = i % 4;
      if ( mmod == 3){
        W[t] = ((uchar)  plain_key[msg_pad + t * 4]) << 24;
        W[t] |= ((uchar) plain_key[msg_pad + t * 4 + 1]) << 16;
        W[t] |= ((uchar) plain_key[msg_pad + t * 4 + 2]) << 8;
        W[t] |=  ((uchar) 0x80) ;
      } else if (mmod == 2) {
        W[t] = ((uchar)  plain_key[msg_pad + t * 4]) << 24;
        W[t] |= ((uchar) plain_key[msg_pad + t * 4 + 1]) << 16;
        W[t] |=  0x8000 ;
      } else if (mmod == 1) {
        W[t] = ((uchar)  plain_key[msg_pad + t * 4]) << 24;
        W[t] |=  0x800000 ;
      } else /*if (mmod == 0)*/ {
        W[t] =  0x80000000 ;
      }
      
      if (current_pad<56)
      {
        W[15] =  ulen*8 ;
        //printf("ulen avlue 2 :w[15] :%u\n", W[15]);
      }
    }
    else if(current_pad <0)
    {
      if( ulen%64==0)
        W[0]=0x80000000;
      W[15]=ulen*8;
      //printf("ulen avlue 3 :w[15] :%u\n", W[15]);
    }

    for (t = 0; t < 64; t++) {
      if (t >= 16)
        W[t] = gamma1(W[t - 2]) + W[t - 7] + gamma0(W[t - 15]) + W[t - 16];
      T1 = H + sigma1(E) + ch(E, F, G) + K[t] + W[t];
      T2 = sigma0(A) + maj(A, B, C);
      H = G; G = F; F = E; E = D + T1; D = C; C = B; B = A; A = T1 + T2;
    }
    digest[0] += A;
    digest[1] += B;
    digest[2] += C;
    digest[3] += D;
    digest[4] += E;
    digest[5] += F;
    digest[6] += G;
    digest[7] += H;

  //  for (t = 0; t < 80; t++)
  //    {
  //    printf("W[%d]: %u\n",t,W[t]);
  //    }
  }

}

__kernel void sha256_crypt_and_worder(__global char *buffer,
                                      __global char *salt_before,
                                      __global uint *size_salt_before,
                                      __global char *salt_after,
                                      __global uint *size_salt_after,
                                      __global uint *number_of_wildcards,
                                      __global char *matching_wild_card,
                                      __global char *char_set,
                                      __global uint *size_char_set,
                                      __global uint *digest_expected
                                      ) {

  int index=get_global_id(0);
  int i;

  int word_size = size_salt_before[0] + size_salt_after[0] + number_of_wildcards[0];
  global char *my_buffer = &buffer[index * word_size];

  for (i=0;i < word_size;i++) {
    if (i < size_salt_before[0]) {
      my_buffer[i] = salt_before[i];
    } else if (i < size_salt_before[0] + number_of_wildcards[0]) {
      int pos = number_of_wildcards[0] - (i - size_salt_before[0]) - 1;
      int base = size_char_set[0];
      int div =  index / custom_pow(base, pos);
      int current =  div % base;
      my_buffer[i] = char_set[current];
    } else {
      my_buffer[i] = salt_after[i - size_salt_before[0] - number_of_wildcards[0]];
    }
  }

  uint current_digest[32];
  hash(my_buffer, current_digest, word_size);
  
  if (comp_arrays(current_digest, digest_expected, 32) == 1) {
    global char *r = &matching_wild_card[0];
    int j;
    for (j=0;j<number_of_wildcards[0];j++) {
      matching_wild_card[j] = my_buffer[size_salt_before[0]+j];
    }
  }
}


__kernel void sha256_crypt_kernel(__global uint *data_info,__global char *plain_keyMulti,  __global uint *digestMulti){

  int index=get_global_id(0);

  global char *plain_key=&plain_keyMulti[data_info[index]];
  global uint *digest=&digestMulti[index*8];
  uint ulen = data_info[index+1] - data_info[index];

  uint current_digest[32];
  hash(plain_key, current_digest, ulen);

  int i;
  for(i=0;i<32;i++) {
    digest[i] = current_digest[i];
  }

}
`
